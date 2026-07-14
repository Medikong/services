from datetime import UTC, datetime

from contracts import PaymentApprovedEvent, PaymentFailedEvent, RefundCompletedEvent
from sqlalchemy.dialects.postgresql import insert
from sqlalchemy.ext.asyncio import AsyncSession

from app.records import ProcessedEventRecord


async def record_processed_event(
    session: AsyncSession,
    event: PaymentApprovedEvent | PaymentFailedEvent | RefundCompletedEvent,
) -> bool:
    statement = (
        insert(ProcessedEventRecord)
        .values(
            event_id=event.eventId,
            event_type=event.eventType,
            aggregate_type="order",
            aggregate_id=event.orderId,
            processed_at=datetime.now(UTC),
        )
        .on_conflict_do_nothing(index_elements=[ProcessedEventRecord.event_id])
        .returning(ProcessedEventRecord.event_id)
    )
    result = await session.execute(statement)
    return result.scalar_one_or_none() is not None
