from uuid import uuid4

from fastapi.testclient import TestClient

from app.main import create_app


def test_reservation_create_list_cancel_and_expire_conflict_flow() -> None:
    """예약 생성, 중복 예약 충돌, 목록 조회, 취소 후 만료 실패 흐름을 API 레벨에서 검증한다."""
    client = TestClient(create_app())
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
