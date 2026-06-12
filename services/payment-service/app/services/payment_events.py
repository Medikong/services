import asyncio
import logging
from collections.abc import Callable
from dataclasses import dataclass
from datetime import UTC, datetime
from typing import assert_never
from uuid import uuid4

from aiokafka.errors import KafkaError
from contracts.events import PaymentApprovedEvent, PaymentFailedEvent
from kafka_utils import build_producer_headers, start_producer_span
from metrics import MetricResult
from observability import TraceContext, set_current_span_attributes, start_trace_span
from sqlalchemy.orm import Session

from app.auth import UserContext
from app.config import settings
from app.kafka import KafkaProducer
from app.metrics import PaymentEventType
from app.metrics.events import PaymentEventPublishRecorded
from app.metrics.recorder import PaymentTelemetryRecorder
from app.models import Payment, PaymentEvent
from app.schemas import CreatePaymentRequest


logger = logging.getLogger(__name__)
PUBLISH_STATUS_PENDING = "pending"
PUBLISH_STATUS_PUBLISHED = "published"
PUBLISH_STATUS_FAILED = "failed"
DEFAULT_MAX_PUBLISH_ATTEMPTS = 3


@dataclass(frozen=True)
class PaymentEventDraft:
    event_id: str
    event_type: PaymentEventType
    payload: dict
    trace_context: dict | None


class PaymentEventDispatcher:
    def __init__(
        self,
        *,
        db: Session,
        telemetry: PaymentTelemetryRecorder,
        max_attempts: int = DEFAULT_MAX_PUBLISH_ATTEMPTS,
    ) -> None:
        """pending outbox 이벤트를 Kafka로 발행할 의존성을 보관한다."""
        if max_attempts < 1:
            raise ValueError("max_attempts must be greater than 0")
        self._db = db
        self._telemetry = telemetry
        self._max_attempts = max_attempts

    async def dispatch_pending(
        self,
        *,
        kafka_producer: KafkaProducer,
        limit: int = 100,
    ) -> int:
        """pending 이벤트를 조회해 순서대로 Kafka에 발행한다."""
        if limit < 1:
            raise ValueError("limit must be greater than 0")

        with start_trace_span(
            "payment.outbox.dispatch_pending",
            {
                "app.component": "payment_outbox_dispatcher",
                "app.operation": "dispatch_pending",
                "payment.outbox.batch_size": limit,
            },
        ):
            events = (
                self._db.query(PaymentEvent)
                .filter(PaymentEvent.publish_status == PUBLISH_STATUS_PENDING)
                .order_by(PaymentEvent.created_at, PaymentEvent.id)
                .limit(limit)
                .all()
            )
            set_current_span_attributes({"payment.outbox.events.count": len(events)})

            published_count = 0
            for event in events:
                await self.dispatch_event(event=event, kafka_producer=kafka_producer)
                published_count += 1

            set_current_span_attributes({"payment.outbox.events.published": published_count})
            return published_count

    async def dispatch_event(
        self,
        *,
        event: PaymentEvent,
        kafka_producer: KafkaProducer,
    ) -> None:
        """단일 outbox 이벤트를 Kafka에 발행하고 상태를 갱신한다."""
        if kafka_producer is None:
            raise RuntimeError("kafka producer is not configured")

        with start_trace_span(
            "payment.outbox.dispatch_event",
            {
                "app.component": "payment_outbox_dispatcher",
                "app.operation": "dispatch_event",
                "payment.event_type": event.event_type,
            },
        ):
            event_type = PaymentEventType(event.event_type)
            topic = _payment_event_topic(event_type)
            trace_carrier = _trace_carrier(event.trace_context)
            try:
                with start_producer_span(
                    topic,
                    carrier=trace_carrier,
                    attributes={"payment.event_type": event.event_type},
                ):
                    await kafka_producer.send_and_wait(
                        topic,
                        event.payload,
                        headers=_producer_headers(
                            correlation_id=event.payload.get("correlationId"),
                            fallback_carrier=trace_carrier,
                        ),
                    )
            except (KafkaError, RuntimeError) as exc:
                self._mark_failed(event, exc)
                self._telemetry.record(
                    PaymentEventPublishRecorded(event_type=event_type, result=MetricResult.FAILURE)
                )
                set_current_span_attributes({"payment.outbox.publish.result": "failure"})
                raise

            event.publish_attempts += 1
            event.publish_status = PUBLISH_STATUS_PUBLISHED
            event.published_at = datetime.now(UTC)
            event.last_publish_error = None

            self._db.commit()
            self._telemetry.record(
                PaymentEventPublishRecorded(event_type=event_type, result=MetricResult.SUCCESS)
            )
            set_current_span_attributes({"payment.outbox.publish.result": "success"})

    def _mark_failed(self, event: PaymentEvent, exc: KafkaError | RuntimeError) -> None:
        """실패한 발행 시도의 outbox 상태를 저장한다."""
        event.publish_attempts += 1
        if event.publish_attempts >= self._max_attempts:
            event.publish_status = PUBLISH_STATUS_FAILED
        else:
            event.publish_status = PUBLISH_STATUS_PENDING
        event.last_publish_error = _summarize_publish_error(exc)
        self._db.commit()


async def run_payment_event_dispatcher(
    stop_event: asyncio.Event,
    *,
    session_factory: Callable[[], Session],
    kafka_producer: KafkaProducer,
    interval_seconds: float,
    batch_size: int,
    max_attempts: int = DEFAULT_MAX_PUBLISH_ATTEMPTS,
) -> None:
    """애플리케이션 실행 중 pending outbox 이벤트를 주기적으로 발행한다."""
    if kafka_producer is None:
        raise RuntimeError("kafka producer is not configured")
    if interval_seconds <= 0:
        raise ValueError("interval_seconds must be greater than 0")
    if batch_size < 1:
        raise ValueError("batch_size must be greater than 0")

    while not stop_event.is_set():
        try:
            with session_factory() as db:
                dispatcher = PaymentEventDispatcher(
                    db=db,
                    telemetry=PaymentTelemetryRecorder(),
                    max_attempts=max_attempts,
                )
                await dispatcher.dispatch_pending(kafka_producer=kafka_producer, limit=batch_size)
        except (KafkaError, RuntimeError):
            logger.exception("payment_event_dispatch_failed")
        try:
            await asyncio.wait_for(stop_event.wait(), timeout=interval_seconds)
        except TimeoutError:
            pass


def payment_event_type_for_status(payment_status: str) -> PaymentEventType | None:
    """결제 상태에 대응하는 외부 발행 이벤트 타입을 반환한다."""
    # 승인/실패 상태만 외부 서비스가 소비할 결제 이벤트가 된다.
    if payment_status == "approved":
        return PaymentEventType.APPROVED
    if payment_status == "failed":
        return PaymentEventType.FAILED
    return None


def build_payment_event_draft(
    *,
    event_type: PaymentEventType,
    payment: Payment,
    request_body: CreatePaymentRequest,
    user: UserContext,
    correlation_id: str | None,
    trace_context: TraceContext | None = None,
) -> PaymentEventDraft:
    """outbox 저장과 Kafka 발행에 사용할 결제 이벤트 초안을 만든다."""
    event_id = f"evt-{uuid4()}"
    event_kwargs = {
        "eventId": event_id,
        "userId": str(user.user_id),
        "sourceId": payment.id,
        "paymentId": payment.id,
        "reservationId": payment.reservation_id,
        "concertId": payment.concert_id,
        "seatId": request_body.seatId or "unknown",
        "amount": payment.amount,
        "occurredAt": datetime.now(UTC),
        "producer": settings.service_name,
        "correlationId": correlation_id,
    }
    match event_type:
        case PaymentEventType.APPROVED:
            event = PaymentApprovedEvent(**event_kwargs)
        case PaymentEventType.FAILED:
            event = PaymentFailedEvent(**event_kwargs)
        case unreachable:
            assert_never(unreachable)

    # payload에는 추적용 ID를 남기지만 metric label로는 보내지 않는다.
    return PaymentEventDraft(
        event_id=event_id,
        event_type=event_type,
        payload=event.model_dump(mode="json"),
        trace_context=trace_context.as_dict() if trace_context is not None else None,
    )


def _trace_carrier(trace_context: object) -> dict[str, str] | None:
    if not isinstance(trace_context, dict):
        return None
    carrier = trace_context.get("carrier")
    if not isinstance(carrier, dict):
        return None
    resolved = {
        key: value
        for key, value in carrier.items()
        if isinstance(key, str) and isinstance(value, str) and key.strip() and value.strip()
    }
    return resolved or None


def _producer_headers(*, correlation_id: str | None, fallback_carrier: dict[str, str] | None) -> list[tuple[str, bytes]]:
    headers = build_producer_headers(correlation_id=correlation_id)
    if _has_traceparent(headers) or fallback_carrier is None:
        return headers
    return build_producer_headers(correlation_id=correlation_id, carrier=fallback_carrier)


def _has_traceparent(headers: list[tuple[str, bytes]]) -> bool:
    return any(key == "traceparent" for key, _value in headers)


def _payment_event_topic(event_type: PaymentEventType) -> str:
    """이벤트 타입에 대응하는 Kafka topic 이름을 반환한다."""
    # topic 이름은 설정값을 우선 사용한다.
    if event_type is PaymentEventType.APPROVED:
        return settings.payment_approved_topic
    if event_type is PaymentEventType.FAILED:
        return settings.payment_failed_topic
    return event_type.value


def _summarize_publish_error(exc: KafkaError | RuntimeError) -> str:
    """outbox에 저장할 발행 오류 요약을 만든다."""
    summary = str(exc) or exc.__class__.__name__
    return summary[:500]
