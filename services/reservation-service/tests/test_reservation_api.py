import json
import logging
from dataclasses import dataclass, field
from uuid import UUID

from contracts.events import ReservationCreatedEvent, ReservationExpiredEvent
from fastapi.testclient import TestClient
from kafka_utils import KafkaProducerOption
from server.ids import deterministic_uuid_string

from app.kafka import get_kafka_producer
from app.main import create_app


def uuid_id(*parts: object) -> str:
    return deterministic_uuid_string("reservation-api-test", *parts)


def test_reservation_create_list_cancel_and_expire_conflict_flow() -> None:
    """예약 생성, 중복 예약 충돌, 목록 조회, 취소 후 만료 실패 흐름을 API 레벨에서 검증한다."""
    producer = FakeKafkaProducer()
    app = create_app()
    app.dependency_overrides[get_kafka_producer] = lambda: producer
    client = TestClient(app)
    concert_id = uuid_id("concert", "api-flow")
    showtime_id = uuid_id("showtime", "api-flow")
    performance_id = uuid_id("performance", "api-flow")
    seat_id = uuid_id("seat", "api-flow")

    created = client.post(
        "/reservations",
        json={"concertId": concert_id, "showtimeId": showtime_id, "performanceId": performance_id, "seatId": seat_id},
        headers={"X-User-Id": "user-api"},
    ).json()
    duplicate = client.post(
        "/reservations",
        json={"concertId": concert_id, "showtimeId": showtime_id, "performanceId": performance_id, "seatId": seat_id},
        headers={"X-User-Id": "user-api"},
    )
    listed = client.get("/reservations/me", headers={"X-User-Id": "user-api"}).json()
    canceled = client.post(f"/reservations/{created['id']}/cancel").json()
    expire_after_cancel = client.post(f"/reservations/{created['id']}/expire")

    assert created["status"] == "pending"
    UUID(created["id"])
    UUID(producer.sent[0][1]["eventId"])
    assert duplicate.status_code == 409
    assert listed["items"][0]["id"] == created["id"]
    assert canceled["status"] == "canceled"
    assert expire_after_cancel.status_code == 409
    assert producer.sent[0][0] == "reservation-created"
    assert producer.sent[0][1]["reservationId"] == created["id"]
    assert ReservationCreatedEvent.model_validate(producer.sent[0][1]).reservationId == created["id"]
    assert producer.sent[0][2] == []
    assert producer.options_sent[0].correlation_id == producer.sent[0][1]["correlationId"]


def test_duplicate_seat_conflict_logs_domain_rejection_without_exception_event(caplog) -> None:
    """좌석 중복 예약은 409를 유지하되 시스템 예외 로그로 분류하지 않는다."""
    producer = FakeKafkaProducer()
    app = create_app()
    app.dependency_overrides[get_kafka_producer] = lambda: producer
    client = TestClient(app)
    payload = {
        "concertId": uuid_id("concert", "observation"),
        "showtimeId": uuid_id("showtime", "observation"),
        "performanceId": uuid_id("performance", "observation"),
        "seatId": uuid_id("seat", "observation"),
    }
    caplog.set_level(logging.INFO)

    created = client.post("/reservations", json=payload, headers={"X-User-Id": "user-observation-1"})
    duplicate = client.post(
        "/reservations",
        json=payload,
        headers={"X-User-Id": "user-observation-2", "X-Request-Id": "req-duplicate-seat"},
    )

    duplicate_logs = [
        log
        for log in _json_logs(caplog.records, "reservation-service")
        if log.get("request_id") == "req-duplicate-seat"
    ]

    assert created.status_code == 201
    assert duplicate.status_code == 409
    assert duplicate.json()["error"]["code"] == "reservation.conflict"
    assert any(log.get("event") == "domain.rejection.recorded" for log in duplicate_logs)
    assert not any(log.get("event") == "exception.recorded" for log in duplicate_logs)
    rejection_log = next(log for log in duplicate_logs if log.get("event") == "domain.rejection.recorded")
    assert rejection_log["severity_text"] == "INFO"
    assert rejection_log["error.kind"] == "domain_rejection"
    assert rejection_log["error.type"] == "SeatAlreadyReservedError"
    assert rejection_log["http.status_code"] == 409
    assert "exception.stacktrace" not in rejection_log


def test_reservation_expire_publishes_event() -> None:
    """예약 만료 시 알림 서비스가 소비할 reservation-expired 이벤트를 발행한다."""
    producer = FakeKafkaProducer()
    app = create_app()
    app.dependency_overrides[get_kafka_producer] = lambda: producer
    client = TestClient(app)
    created = client.post(
        "/reservations",
        json={
            "concertId": uuid_id("concert", "expire"),
            "showtimeId": uuid_id("showtime", "expire"),
            "performanceId": uuid_id("performance", "expire"),
            "seatId": uuid_id("seat", "expire"),
        },
        headers={"X-User-Id": "1"},
    ).json()

    expired = client.post(f"/reservations/{created['id']}/expire").json()

    assert expired["status"] == "expired"
    assert [item[0] for item in producer.sent] == ["reservation-created", "reservation-expired"]
    assert producer.sent[1][1]["reservationId"] == created["id"]
    assert producer.sent[0][1]["userId"] == "1"
    assert producer.sent[1][1]["userId"] == "1"
    assert ReservationExpiredEvent.model_validate(producer.sent[1][1]).userId == "1"
    assert [options.correlation_id for options in producer.options_sent] == [
        producer.sent[0][1]["correlationId"],
        producer.sent[1][1]["correlationId"],
    ]


def test_sales_and_policy_admin_flow() -> None:
    """판매 상태 변경과 대기열/트래픽 정책 갱신 API 흐름을 검증한다."""
    client = TestClient(create_app())
    concert_id = uuid_id("concert", "sales-api")

    started = client.post(f"/admin/concerts/{concert_id}/sales/start").json()
    paused = client.post(f"/admin/concerts/{concert_id}/sales/pause").json()
    resumed = client.post(f"/admin/concerts/{concert_id}/sales/resume").json()
    queue_policy = client.post(
        f"/admin/concerts/{concert_id}/queue-policy",
        json={"enabled": True, "maxEntrantsPerMinute": 100, "waitingRoomUrl": "https://queue.example.com"},
    ).json()
    traffic_policy = client.post(
        f"/admin/concerts/{concert_id}/traffic-policy",
        json={"macroProtectionEnabled": True, "maxRequestsPerUserPerMinute": 30, "blockSuspiciousTraffic": True},
    ).json()

    assert started["salesStatus"] == "open"
    assert paused["salesStatus"] == "paused"
    assert resumed["salesStatus"] == "open"
    assert queue_policy["enabled"] is True
    assert traffic_policy["macroProtectionEnabled"] is True


def test_error_response_uses_common_shape() -> None:
    """예약 서비스 오류 응답이 공통 에러 형식을 따르는지 검증한다."""
    client = TestClient(create_app())

    response = client.get(f"/reservations/{uuid_id('reservation', 'missing')}", headers={"X-Request-Id": "req-reservation"})

    assert response.status_code == 404
    assert response.json()["requestId"] == "req-reservation"
    assert response.json()["error"]["code"] == "reservation.not_found"


def test_reservation_metrics_record_command_conflict_sales_and_event_publish_results() -> None:
    """예약 P1 metric이 /metrics에 저카디널리티 label로 노출되는지 검증한다."""
    producer = FakeKafkaProducer()
    app = create_app()
    app.dependency_overrides[get_kafka_producer] = lambda: producer
    client = TestClient(app)
    concert_id = uuid_id("concert", "metrics")
    showtime_id = uuid_id("showtime", "metrics")
    performance_id = uuid_id("performance", "metrics")
    seat_id = uuid_id("seat", "metrics")

    created = client.post(
        "/reservations",
        json={"concertId": concert_id, "showtimeId": showtime_id, "performanceId": performance_id, "seatId": seat_id},
        headers={"X-User-Id": "user-metrics"},
    )
    duplicate = client.post(
        "/reservations",
        json={"concertId": concert_id, "showtimeId": showtime_id, "performanceId": performance_id, "seatId": seat_id},
        headers={"X-User-Id": "user-metrics-2"},
    )
    sales_started = client.post(f"/admin/concerts/{concert_id}/sales/start")
    sales_paused = client.post(f"/admin/concerts/{concert_id}/sales/pause")
    expired = client.post(f"/reservations/{created.json()['id']}/expire")

    assert created.status_code == 201
    assert duplicate.status_code == 409
    assert sales_started.status_code == 200
    assert sales_paused.status_code == 200
    assert expired.status_code == 200

    metrics_text = client.get("/metrics").text
    assert "reservations_total" in metrics_text
    assert 'service_name="reservation-service"' in metrics_text
    assert 'service_environment="test"' in metrics_text
    assert 'result="success"' in metrics_text
    assert 'result="rejection"' in metrics_text
    assert 'error_code="none"' in metrics_text
    assert 'error_code="reservation.conflict"' in metrics_text
    assert 'failure_kind="business_rejection"' in metrics_text
    assert 'expected="true"' in metrics_text
    assert "reservation_command_duration_seconds_bucket" in metrics_text
    assert 'command="create"' in metrics_text
    assert 'command="expire"' in metrics_text
    assert "reservation_conflicts_total" in metrics_text
    assert 'conflict_type="seat_conflict"' in metrics_text
    assert "sales_state_changes_total" in metrics_text
    assert 'action="start"' in metrics_text
    assert 'action="pause"' in metrics_text
    assert "reservation_events_published_total" in metrics_text
    assert 'event_type="reservation-created"' in metrics_text
    assert 'event_type="reservation-expired"' in metrics_text
    assert_no_high_cardinality_metric_labels(metrics_text)


def test_reservation_event_publish_failure_metric_preserves_error_flow() -> None:
    """예약 이벤트 발행 실패 metric이 예외 흐름을 삼키지 않는지 검증한다."""
    app = create_app()
    app.dependency_overrides[get_kafka_producer] = lambda: FailingKafkaProducer()
    client = TestClient(app)
    response = client.post(
        "/reservations",
        json={
            "concertId": uuid_id("concert", "publish-failure"),
            "showtimeId": uuid_id("showtime", "publish-failure"),
            "performanceId": uuid_id("performance", "publish-failure"),
            "seatId": uuid_id("seat", "publish-failure"),
        },
        headers={"X-User-Id": "user-publish-failure"},
    )

    assert response.status_code == 500
    metrics_text = client.get("/metrics").text
    assert "reservation_events_published_total" in metrics_text
    assert 'event_type="reservation-created"' in metrics_text
    assert 'result="failure"' in metrics_text
    assert_no_high_cardinality_metric_labels(metrics_text)


def assert_no_high_cardinality_metric_labels(metrics_text: str) -> None:
    forbidden_labels = (
        "request_id",
        "trace_id",
        "span_id",
        "correlation_id",
        "user_id",
        "payment_id",
        "reservation_id",
        "ticket_id",
        "raw_path",
    )
    for label in forbidden_labels:
        assert f"{label}=" not in metrics_text


def _json_logs(records: list[logging.LogRecord], logger_name: str) -> list[dict[str, object]]:
    logs: list[dict[str, object]] = []
    for record in records:
        if record.name != logger_name or not record.message.startswith("{"):
            continue
        logs.append(json.loads(record.message))
    return logs


class FakeKafkaProducer:
    def __init__(self) -> None:
        self.sent: list[tuple[str, dict, list[tuple[str, bytes]]]] = []
        self.options_sent: list[RecordedKafkaOptions] = []

    async def send_and_wait(
        self,
        topic: str,
        payload: dict,
        *producer_options: KafkaProducerOption,
        headers: list[tuple[str, bytes]] | None = None,
    ) -> None:
        options = RecordedKafkaOptions()
        for producer_option in producer_options:
            producer_option(options)
        self.options_sent.append(options)
        self.sent.append((topic, payload, list(headers or [])))


class FailingKafkaProducer:
    async def send_and_wait(
        self,
        topic: str,
        payload: dict,
        *producer_options: KafkaProducerOption,
        headers: list[tuple[str, bytes]] | None = None,
    ) -> None:
        raise RuntimeError("kafka publish failed")


@dataclass
class RecordedKafkaOptions:
    trace_context: dict | None = None
    trace_carrier: dict | None = None
    correlation_id: str | None = None
    span_name: str | None = None
    span_attributes: dict[str, object] = field(default_factory=dict)
