from datetime import UTC, datetime
from uuid import uuid4

from contracts import OrderCreatedEvent
from sqlalchemy import (
    CheckConstraint,
    DateTime,
    ForeignKeyConstraint,
    Index,
    Integer,
    String,
    UniqueConstraint,
    select,
)
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker
from sqlalchemy.orm import DeclarativeBase, Mapped, mapped_column

from app.models import OrderId, Payment, PaymentId, PaymentMethod, PaymentStatus, UserId
from app.store import (
    ApprovePaymentCommand,
    ApprovePaymentResult,
    FailPaymentCommand,
    FailPaymentResult,
    KnownOrder,
    PaymentAlreadyFailed,
    PaymentAlreadyApproved,
    PaymentApproved,
    PaymentFailed,
    PaymentIdempotencyConflict,
    PaymentOrderMismatch,
    PaymentOrderNotFound,
    failed_payment_matches_command,
    known_order_matches_command,
    payment_matches_command,
)


class Base(DeclarativeBase):
    pass


class PaymentRecord(Base):
    __tablename__ = "payments"
    __table_args__ = (
        CheckConstraint("amount >= 0", name="ck_payments_amount_nonnegative"),
        CheckConstraint("method = 'MOCK_CARD'", name="ck_payments_method"),
        CheckConstraint(
            "status IN ('APPROVED', 'FAILED')",
            name="ck_payments_status",
        ),
        CheckConstraint(
            "(status = 'APPROVED' AND approved_at IS NOT NULL AND failed_at IS NULL) "
            "OR (status = 'FAILED' AND approved_at IS NULL AND failed_at IS NOT NULL)",
            name="ck_payments_terminal_timestamps",
        ),
        ForeignKeyConstraint(
            ["order_id"],
            ["known_orders.order_id"],
            name="fk_payments_order_id",
        ),
        UniqueConstraint(
            "user_id",
            "idempotency_key",
            name="uq_payments_user_idempotency_key",
        ),
        UniqueConstraint("order_id", name="uq_payments_order_id"),
        Index("ix_payments_order_id", "order_id"),
    )

    id: Mapped[str] = mapped_column(String(64), primary_key=True)
    order_id: Mapped[str] = mapped_column(String(64), nullable=False)
    user_id: Mapped[str] = mapped_column(String(64), nullable=False)
    amount: Mapped[int] = mapped_column(Integer, nullable=False)
    method: Mapped[str] = mapped_column(String(32), nullable=False)
    status: Mapped[str] = mapped_column(String(32), nullable=False)
    idempotency_key: Mapped[str] = mapped_column(String(128), nullable=False)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    approved_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    failed_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    failure_reason: Mapped[str | None] = mapped_column(String(128), nullable=True)


class KnownOrderRecord(Base):
    __tablename__ = "known_orders"
    __table_args__ = (
        CheckConstraint("amount >= 0", name="ck_known_orders_amount_nonnegative"),
    )

    order_id: Mapped[str] = mapped_column(String(64), primary_key=True)
    user_id: Mapped[str] = mapped_column(String(64), nullable=False)
    amount: Mapped[int] = mapped_column(Integer, nullable=False)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)


class PostgresPaymentRepository:
    def __init__(self, session_factory: async_sessionmaker[AsyncSession]) -> None:
        self._session_factory = session_factory

    async def approve_mock_payment(self, command: ApprovePaymentCommand) -> ApprovePaymentResult:
        async with self._session_factory() as session:
            replayed_payment = await self._replayed_payment(session, command)
            if replayed_payment is not None:
                if not payment_matches_command(replayed_payment, command):
                    return PaymentIdempotencyConflict(payment=replayed_payment)
                return PaymentAlreadyApproved(payment=replayed_payment)

            known_order_record = await session.get(KnownOrderRecord, command.order_id)
            if known_order_record is None:
                return PaymentOrderNotFound(order_id=command.order_id)
            known_order = _known_order_from_record(known_order_record)
            if not known_order_matches_command(known_order, command):
                return PaymentOrderMismatch(order_id=command.order_id)

            approved_at = datetime.now(UTC)
            record = PaymentRecord(
                id=_new_payment_id(),
                order_id=command.order_id,
                user_id=command.user_id,
                amount=command.amount,
                method=command.method.value,
                status=PaymentStatus.APPROVED.value,
                idempotency_key=command.idempotency_key,
                created_at=approved_at,
                approved_at=approved_at,
                failed_at=None,
                failure_reason=None,
            )
            session.add(record)
            try:
                await session.commit()
            except IntegrityError:
                await session.rollback()
                replayed_after_conflict = await self._replayed_payment(session, command)
                if replayed_after_conflict is not None:
                    if not payment_matches_command(replayed_after_conflict, command):
                        return PaymentIdempotencyConflict(payment=replayed_after_conflict)
                    return PaymentAlreadyApproved(payment=replayed_after_conflict)
                raise
            return PaymentApproved(payment=_payment_from_record(record))

    async def fail_mock_payment(self, command: FailPaymentCommand) -> FailPaymentResult:
        async with self._session_factory() as session:
            replayed_payment = await self._replayed_payment(session, command)
            if replayed_payment is not None:
                if not failed_payment_matches_command(replayed_payment, command):
                    return PaymentIdempotencyConflict(payment=replayed_payment)
                return PaymentAlreadyFailed(payment=replayed_payment)

            known_order_record = await session.get(KnownOrderRecord, command.order_id)
            if known_order_record is None:
                return PaymentOrderNotFound(order_id=command.order_id)
            known_order = _known_order_from_record(known_order_record)
            if not known_order_matches_command(known_order, command):
                return PaymentOrderMismatch(order_id=command.order_id)

            failed_at = datetime.now(UTC)
            record = PaymentRecord(
                id=_new_payment_id(),
                order_id=command.order_id,
                user_id=command.user_id,
                amount=command.amount,
                method=command.method.value,
                status=PaymentStatus.FAILED.value,
                idempotency_key=command.idempotency_key,
                created_at=failed_at,
                approved_at=None,
                failed_at=failed_at,
                failure_reason=command.reason,
            )
            session.add(record)
            try:
                await session.commit()
            except IntegrityError:
                await session.rollback()
                replayed_after_conflict = await self._replayed_payment(session, command)
                if replayed_after_conflict is not None:
                    if not failed_payment_matches_command(replayed_after_conflict, command):
                        return PaymentIdempotencyConflict(payment=replayed_after_conflict)
                    return PaymentAlreadyFailed(payment=replayed_after_conflict)
                raise
            return PaymentFailed(payment=_payment_from_record(record))

    async def get_payment(self, payment_id: PaymentId) -> Payment | None:
        async with self._session_factory() as session:
            record = await session.get(PaymentRecord, payment_id)
            if record is None:
                return None
            return _payment_from_record(record)

    async def record_order_created(self, event: OrderCreatedEvent) -> KnownOrder:
        async with self._session_factory() as session:
            record = await session.get(KnownOrderRecord, event.orderId)
            if record is None:
                record = KnownOrderRecord(
                    order_id=event.orderId,
                    user_id=event.userId,
                    amount=event.amount,
                    created_at=event.occurredAt,
                )
                session.add(record)
            else:
                record.user_id = event.userId
                record.amount = event.amount
                record.created_at = event.occurredAt
            await session.commit()
            return _known_order_from_record(record)

    async def get_known_order(self, order_id: str) -> KnownOrder | None:
        async with self._session_factory() as session:
            record = await session.get(KnownOrderRecord, order_id)
            if record is None:
                return None
            return _known_order_from_record(record)

    async def _replayed_payment(
        self,
        session: AsyncSession,
        command: ApprovePaymentCommand | FailPaymentCommand,
    ) -> Payment | None:
        result = await session.execute(
            select(PaymentRecord).where(
                PaymentRecord.user_id == command.user_id,
                PaymentRecord.idempotency_key == command.idempotency_key,
            ),
        )
        record = result.scalar_one_or_none()
        if record is None:
            return None
        return _payment_from_record(record)


def _new_payment_id() -> PaymentId:
    return PaymentId(f"payment-{uuid4().hex[:12]}")


def _payment_from_record(record: PaymentRecord) -> Payment:
    return Payment(
        id=record.id,
        orderId=record.order_id,
        userId=record.user_id,
        amount=record.amount,
        method=PaymentMethod(record.method),
        status=PaymentStatus(record.status),
        createdAt=record.created_at,
        approvedAt=record.approved_at,
        failedAt=record.failed_at,
        failureReason=record.failure_reason,
    )


def _known_order_from_record(record: KnownOrderRecord) -> KnownOrder:
    return KnownOrder(
        order_id=OrderId(record.order_id),
        user_id=UserId(record.user_id),
        amount=record.amount,
    )
