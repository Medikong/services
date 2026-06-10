from uuid import uuid4

from fastapi.testclient import TestClient

from app.kafka import get_kafka_producer
from app.main import create_app


def test_reservation_create_list_cancel_and_expire_conflict_flow() -> None:
    """예약 생성, 중복 예약 충돌, 목록 조회, 취소 후 만료 실패 흐름을 API 레벨에서 검증한다."""
    producer = FakeKafkaProducer()
    app = create_app()
    app.dependency_overrides[get_kafka_producer] = lambda: producer
    client = TestClient(app)
    suffix = uuid4().hex[:8]
    concert_id = f"concert-api-flow-{suffix}"
    showtime_id = f"showtime-api-flow-{suffix}"
    performance_id = f"perf-api-flow-{suffix}"

    created = client.post(
        "/reservations",
        json={"concertId": concert_id, "showtimeId": showtime_id, "performanceId": performance_id, "seatId": "A-1"},
        headers={"X-User-Id": "user-api"},
    ).json()
    duplicate = client.post(
        "/reservations",
        json={"concertId": concert_id, "showtimeId": showtime_id, "performanceId": performance_id, "seatId": "A-1"},
        headers={"X-User-Id": "user-api"},
    )
    listed = client.get("/reservations/me", headers={"X-User-Id": "user-api"}).json()
    canceled = client.post(f"/reservations/{created['id']}/cancel").json()
    expire_after_cancel = client.post(f"/reservations/{created['id']}/expire")

    assert created["status"] == "pending"
    assert duplicate.status_code == 409
    assert listed["items"][0]["id"] == created["id"]
    assert canceled["status"] == "canceled"
    assert expire_after_cancel.status_code == 409
    assert producer.sent[0][0] == "reservation-created"
    assert producer.sent[0][1]["reservationId"] == created["id"]
    assert dict(producer.sent[0][2])["correlation_id"] == producer.sent[0][1]["correlationId"].encode("utf-8")


def test_reservation_expire_publishes_event() -> None:
    """예약 만료 시 알림 서비스가 소비할 reservation-expired 이벤트를 발행한다."""
    producer = FakeKafkaProducer()
    app = create_app()
    app.dependency_overrides[get_kafka_producer] = lambda: producer
    client = TestClient(app)
    suffix = uuid4().hex[:8]

    created = client.post(
        "/reservations",
        json={
            "concertId": f"concert-expire-{suffix}",
            "showtimeId": f"showtime-expire-{suffix}",
            "performanceId": f"perf-expire-{suffix}",
            "seatId": "A-2",
        },
        headers={"X-User-Id": "1"},
    ).json()

    expired = client.post(f"/reservations/{created['id']}/expire").json()

    assert expired["status"] == "expired"
    assert [item[0] for item in producer.sent] == ["reservation-created", "reservation-expired"]
    assert producer.sent[1][1]["reservationId"] == created["id"]
    assert producer.sent[0][1]["userId"] == "1"
    assert producer.sent[1][1]["userId"] == "1"


def test_sales_and_policy_admin_flow() -> None:
    """판매 상태 변경과 대기열/트래픽 정책 갱신 API 흐름을 검증한다."""
    client = TestClient(create_app())
    concert_id = f"concert-sales-api-{uuid4().hex[:8]}"

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

    response = client.get("/reservations/missing-rsv", headers={"X-Request-Id": "req-reservation"})

    assert response.status_code == 404
    assert response.json()["requestId"] == "req-reservation"
    assert response.json()["error"]["code"] == "reservation.not_found"


def test_reservation_metrics_record_command_conflict_sales_and_event_publish_results() -> None:
    """예약 P1 metric이 /metrics에 저카디널리티 label로 노출되는지 검증한다."""
    producer = FakeKafkaProducer()
    app = create_app()
    app.dependency_overrides[get_kafka_producer] = lambda: producer
    client = TestClient(app)
    suffix = uuid4().hex[:8]
    concert_id = f"concert-metrics-{suffix}"
    showtime_id = f"showtime-metrics-{suffix}"
    performance_id = f"perf-metrics-{suffix}"

    created = client.post(
        "/reservations",
        json={"concertId": concert_id, "showtimeId": showtime_id, "performanceId": performance_id, "seatId": "A-1"},
        headers={"X-User-Id": "user-metrics"},
    )
    duplicate = client.post(
        "/reservations",
        json={"concertId": concert_id, "showtimeId": showtime_id, "performanceId": performance_id, "seatId": "A-1"},
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
    suffix = uuid4().hex[:8]

    response = client.post(
        "/reservations",
        json={
            "concertId": f"concert-publish-failure-{suffix}",
            "showtimeId": f"showtime-publish-failure-{suffix}",
            "performanceId": f"perf-publish-failure-{suffix}",
            "seatId": "A-1",
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


class FakeKafkaProducer:
    def __init__(self) -> None:
        self.sent: list[tuple[str, dict, list[tuple[str, bytes]]]] = []

    async def send_and_wait(self, topic: str, payload: dict, *, headers: list[tuple[str, bytes]]) -> None:
        self.sent.append((topic, payload, headers))


class FailingKafkaProducer:
    async def send_and_wait(self, topic: str, payload: dict, *, headers: list[tuple[str, bytes]]) -> None:
        raise RuntimeError("kafka publish failed")
