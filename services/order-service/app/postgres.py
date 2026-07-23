import asyncio
import os
from datetime import datetime
from typing import Final
from uuid import uuid4

from contracts import (
    PaymentApprovedEvent,
    PaymentFailedEvent,
    RefundCompletedEvent,
    RefundFailedEvent,
)
from sqlalchemy import select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from app.catalog import PRODUCTS_FOR_SALE, ProductForSale, product_for
from app.cancellations import RequestCancellationCommand, RequestCancellationResult
from app.events import inventory_changed_event, order_created_event
from app.metrics import OrderMetrics
from app.models import Cancellation, Order, OrderId, OrderStatus, UserId
from app.outbox import add_outbox_event
from app.order_config import OrderPaymentPolicy
from app.postgres_mapping import order_from_record
from app.postgres_cancellations import get_cancellation, request_cancellation
from app.postgres_payments import apply_payment_approved, apply_payment_failed
from app.postgres_inventory import (
    expire_due_order,
    expire_pending_order,
)
from app.postgres_refunds import apply_refund_completed, apply_refund_failed
from app.records import Base as Base
from app.records import InventoryItemRecord, OrderRecord
from app.store import (
    CreateOrderCommand,
    CreateOrderResult,
    OrderAlreadyCreated,
    OrderCreated,
    OrderIdempotencyConflict,
    PaymentApprovalResult,
    PaymentFailureResult,
    ProductSoldOut,
    ProductUnavailable,
    order_matches_command,
)

# 카나리/블루그린 배포전략 비교 실험용 (2026-07-23, experiment/order-service-synthetic-regression
# 브랜치 전용, main엔 merge 안 함). _locked_inventory()의 `.with_for_update()` 행 잠금을
# env var로 우회 가능하게 만든다 — "리팩터링 중 실수로 락을 빠뜨렸다"는 현실적인 회귀를
# 재현해서, 동시 주문 요청이 재고 초과 판매(oversell)를 일으키는지를 카나리/블루그린별로
# 실측 비교하기 위함. SYNTHETIC_REGRESSION_DELAY_SECONDS는 read-check-write 사이 레이스
# 윈도우를 넓혀서 테스트 부하로도 안정적으로 재현되게 하는 용도.
SYNTHETIC_REGRESSION_SKIP_LOCK: Final = (
    os.getenv("SYNTHETIC_REGRESSION_SKIP_LOCK", "false").lower() == "true"
)
SYNTHETIC_REGRESSION_DELAY_SECONDS: Final = float(
    os.getenv("SYNTHETIC_REGRESSION_DELAY_SECONDS", "0")
)


class PostgresOrderRepository:
    def __init__(
        self,
        session_factory: async_sessionmaker[AsyncSession],
        catalog: tuple[ProductForSale, ...] = PRODUCTS_FOR_SALE,
        policy: OrderPaymentPolicy = OrderPaymentPolicy(),
        metrics: OrderMetrics | None = None,
    ) -> None:
        self._session_factory = session_factory
        self._catalog = catalog
        self._policy = policy
        self._metrics = metrics

    async def create_order(self, command: CreateOrderCommand) -> CreateOrderResult:
        async with self._session_factory() as session:
            replayed_order = await self._replayed_order(session, command)
            if replayed_order is not None:
                if not order_matches_command(replayed_order, command):
                    return OrderIdempotencyConflict(order=replayed_order)
                return OrderAlreadyCreated(order=replayed_order)

            product = product_for(self._catalog, command.drop_id, command.product_id)
            if product is None:
                return ProductUnavailable(
                    drop_id=command.drop_id,
                    product_id=command.product_id,
                )

            inventory = await _locked_inventory(session, product)
            if inventory is None:
                return ProductUnavailable(
                    drop_id=command.drop_id,
                    product_id=command.product_id,
                )
            if SYNTHETIC_REGRESSION_DELAY_SECONDS > 0:
                # read-check-write 사이 레이스 윈도우를 인위적으로 넓혀서, 잠금이
                # 빠진 상태의 동시 요청들이 서로의 재고 변경을 못 보고 지나치게 만든다.
                await asyncio.sleep(SYNTHETIC_REGRESSION_DELAY_SECONDS)
            replayed_order = await self._replayed_order(session, command)
            if replayed_order is not None:
                if not order_matches_command(replayed_order, command):
                    return OrderIdempotencyConflict(order=replayed_order)
                return OrderAlreadyCreated(order=replayed_order)

            available_quantity = (
                inventory.total_quantity
                - inventory.reserved_quantity
                - inventory.sold_quantity
            )
            if command.quantity > available_quantity:
                return ProductSoldOut(
                    drop_id=command.drop_id,
                    product_id=command.product_id,
                )

            created_at = self._policy.clock()
            record = OrderRecord(
                id=_new_order_id(),
                user_id=command.user_id,
                drop_id=command.drop_id,
                product_id=command.product_id,
                quantity=command.quantity,
                amount=product.unit_price * command.quantity,
                status=OrderStatus.PENDING_PAYMENT.value,
                idempotency_key=command.idempotency_key,
                created_at=created_at,
                expires_at=created_at + self._policy.ttl,
                fulfillment_status="NOT_STARTED",
            )
            inventory.reserved_quantity += command.quantity
            inventory.version += 1
            session.add(record)
            add_outbox_event(
                session,
                order_created_event(order_from_record(record), command.idempotency_key),
            )
            add_outbox_event(
                session,
                inventory_changed_event(
                    inventory,
                    cause_id=f"reserve:{record.id}",
                    occurred_at=created_at,
                    user_id=record.user_id,
                    order_id=record.id,
                ),
            )
            try:
                await session.commit()
            except IntegrityError:
                await session.rollback()
                replayed_after_conflict = await self._replayed_order(session, command)
                if replayed_after_conflict is not None:
                    if not order_matches_command(replayed_after_conflict, command):
                        return OrderIdempotencyConflict(order=replayed_after_conflict)
                    return OrderAlreadyCreated(order=replayed_after_conflict)
                raise
            return OrderCreated(
                order=order_from_record(record),
                idempotency_key=command.idempotency_key,
            )

    async def get_order(self, order_id: OrderId) -> Order | None:
        async with self._session_factory() as session:
            record = await session.get(OrderRecord, order_id)
            if record is None:
                return None
            return order_from_record(record)

    async def request_cancellation(
        self,
        command: RequestCancellationCommand,
    ) -> RequestCancellationResult:
        return await request_cancellation(self._session_factory, command)

    async def get_cancellation(
        self,
        order_id: OrderId,
        user_id: UserId,
    ) -> Cancellation | None:
        return await get_cancellation(self._session_factory, order_id, user_id)

    async def apply_payment_approved(
        self,
        event: PaymentApprovedEvent,
    ) -> PaymentApprovalResult:
        return await apply_payment_approved(self._session_factory, event)

    async def apply_payment_failed(
        self,
        event: PaymentFailedEvent,
    ) -> PaymentFailureResult:
        return await apply_payment_failed(self._session_factory, event)

    async def expire_pending_order(
        self,
        order_id: OrderId,
        occurred_at: datetime,
    ) -> bool:
        return await expire_pending_order(self._session_factory, order_id, occurred_at)

    async def expire_due_order(self, now: datetime) -> bool:
        return await expire_due_order(self._session_factory, now, self._metrics)

    async def apply_refund_completed(self, event: RefundCompletedEvent) -> bool:
        return await apply_refund_completed(self._session_factory, event)

    async def apply_refund_failed(self, event: RefundFailedEvent) -> bool:
        return await apply_refund_failed(self._session_factory, event)

    async def _replayed_order(
        self,
        session: AsyncSession,
        command: CreateOrderCommand,
    ) -> Order | None:
        result = await session.execute(
            select(OrderRecord).where(
                OrderRecord.user_id == command.user_id,
                OrderRecord.idempotency_key == command.idempotency_key,
            ),
        )
        record = result.scalar_one_or_none()
        if record is None:
            return None
        return order_from_record(record)


async def _locked_inventory(
    session: AsyncSession,
    product: ProductForSale,
) -> InventoryItemRecord | None:
    query = select(InventoryItemRecord).where(
        InventoryItemRecord.drop_id == product.drop_id,
        InventoryItemRecord.product_id == product.product_id,
    )
    if not SYNTHETIC_REGRESSION_SKIP_LOCK:
        query = query.with_for_update()
    result = await session.execute(query)
    return result.scalar_one_or_none()


def _new_order_id() -> OrderId:
    return OrderId(f"order-{uuid4().hex[:12]}")
