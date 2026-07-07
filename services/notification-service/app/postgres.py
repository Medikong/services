from datetime import datetime

from contracts import NotificationRequestedEvent
from sqlalchemy import Boolean, DateTime, Index, String, UniqueConstraint, and_, or_, select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker
from sqlalchemy.orm import DeclarativeBase, Mapped, mapped_column

from app.models import Notification, NotificationId, NotificationType, UserId
from app.store import (
    NotificationAlreadyRecorded,
    NotificationPage,
    NotificationRecorded,
    RecordNotificationResult,
    notification_from_requested_event,
    page_from_notifications,
)


class Base(DeclarativeBase):
    pass


class NotificationRecord(Base):
    __tablename__ = "notifications"
    __table_args__ = (
        UniqueConstraint("event_id", name="uq_notifications_event_id"),
        Index("ix_notifications_user_created", "user_id", "created_at"),
    )

    id: Mapped[str] = mapped_column(String(64), primary_key=True)
    event_id: Mapped[str] = mapped_column(String(128), nullable=False)
    user_id: Mapped[str] = mapped_column(String(64), nullable=False)
    order_id: Mapped[str | None] = mapped_column(String(64), nullable=True)
    type: Mapped[str] = mapped_column(String(32), nullable=False)
    title: Mapped[str] = mapped_column(String(120), nullable=False)
    message: Mapped[str] = mapped_column(String(500), nullable=False)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    read: Mapped[bool] = mapped_column(Boolean, nullable=False)


class PostgresNotificationRepository:
    def __init__(self, session_factory: async_sessionmaker[AsyncSession]) -> None:
        self._session_factory = session_factory

    async def record_notification_requested(
        self,
        event: NotificationRequestedEvent,
    ) -> RecordNotificationResult:
        async with self._session_factory() as session:
            existing = await self._notification_by_event_id(session, event.eventId)
            if existing is not None:
                return NotificationAlreadyRecorded(notification=existing)

            notification = notification_from_requested_event(event)
            record = NotificationRecord(
                id=notification.id,
                event_id=event.eventId,
                user_id=notification.userId,
                order_id=notification.orderId,
                type=notification.type.value,
                title=notification.title,
                message=notification.message,
                created_at=notification.createdAt,
                read=notification.read,
            )
            session.add(record)
            try:
                await session.commit()
            except IntegrityError:
                await session.rollback()
                replayed = await self._notification_by_event_id(session, event.eventId)
                if replayed is not None:
                    return NotificationAlreadyRecorded(notification=replayed)
                raise
            return NotificationRecorded(notification=_notification_from_record(record))

    async def list_notifications(
        self,
        user_id: UserId,
        limit: int,
        cursor: NotificationId | None = None,
    ) -> NotificationPage:
        async with self._session_factory() as session:
            cursor_record = await self._notification_by_id(session, user_id, cursor)
            if cursor is not None and cursor_record is None:
                return page_from_notifications((), limit)

            cursor_filter = (
                ()
                if cursor_record is None
                else (
                    or_(
                        NotificationRecord.created_at < cursor_record.created_at,
                        and_(
                            NotificationRecord.created_at == cursor_record.created_at,
                            NotificationRecord.id < cursor_record.id,
                        ),
                    ),
                )
            )
            result = await session.execute(
                select(NotificationRecord)
                .where(NotificationRecord.user_id == user_id, *cursor_filter)
                .order_by(
                    NotificationRecord.created_at.desc(),
                    NotificationRecord.id.desc(),
                )
                .limit(limit + 1),
            )
            notifications = tuple(_notification_from_record(record) for record in result.scalars().all())
            return page_from_notifications(notifications, limit)

    async def _notification_by_event_id(
        self,
        session: AsyncSession,
        event_id: str,
    ) -> Notification | None:
        result = await session.execute(
            select(NotificationRecord).where(NotificationRecord.event_id == event_id),
        )
        record = result.scalar_one_or_none()
        if record is None:
            return None
        return _notification_from_record(record)

    async def _notification_by_id(
        self,
        session: AsyncSession,
        user_id: UserId,
        notification_id: NotificationId | None,
    ) -> NotificationRecord | None:
        if notification_id is None:
            return None
        result = await session.execute(
            select(NotificationRecord).where(
                NotificationRecord.id == notification_id,
                NotificationRecord.user_id == user_id,
            ),
        )
        return result.scalar_one_or_none()


def _notification_from_record(record: NotificationRecord) -> Notification:
    return Notification(
        id=NotificationId(record.id),
        userId=UserId(record.user_id),
        orderId=record.order_id,
        type=NotificationType(record.type),
        title=record.title,
        message=record.message,
        createdAt=record.created_at,
        read=record.read,
    )
