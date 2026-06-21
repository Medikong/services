from datetime import UTC, datetime, timedelta
from uuid import UUID

from fastapi.testclient import TestClient
from server.ids import deterministic_uuid_string

from app.main import create_app


def uuid_id(*parts: object) -> str:
    return deterministic_uuid_string("concert-api-test", *parts)


def create_public_concert(client: TestClient, title: str = "Spring Live"):
    starts_at = datetime.now(UTC) + timedelta(days=7)
    ends_at = starts_at + timedelta(hours=2)
    venue = client.post("/provider/venues", json={"name": f"{title} Hall", "address": "Seoul", "totalSeats": 2}).json()
    concert = client.post(
        "/provider/concerts",
        json={"title": title, "description": "Public booking test", "ageRating": "ALL", "runningMinutes": 120},
        headers={"X-Provider-Id": "provider-api"},
    ).json()
    showtime = client.post(
        f"/provider/concerts/{concert['id']}/showtimes",
        json={"venueId": venue["id"], "startsAt": starts_at.isoformat(), "endsAt": ends_at.isoformat()},
    ).json()
    seat_map_response = client.post(
        f"/provider/showtimes/{showtime['id']}/seat-map",
        json={"sections": [{"name": "A", "rows": [{"name": "1", "seatNumbers": ["1", "2"]}]}]},
    )
    assert seat_map_response.status_code == 204
    return concert, venue, showtime, starts_at


def test_provider_to_public_concert_flow() -> None:
    client = TestClient(create_app())
    concert, venue, showtime, starts_at = create_public_concert(client)
    performances = client.get(f"/concerts/{concert['id']}/performances").json()
    seats = client.get(f"/performances/{showtime['id']}/seats").json()
    public_concert = client.get(f"/concerts/{concert['id']}").json()
    recommended = client.get("/concerts/recommended?limit=10").json()
    calendar = client.get(f"/concerts/{concert['id']}/calendar?yearMonth={starts_at:%Y-%m}").json()
    date_performances = client.get(f"/concerts/{concert['id']}/dates/{starts_at:%Y-%m-%d}/performances").json()
    seat_map = client.get(f"/performances/{showtime['id']}/seat-map").json()
    metrics = client.get("/metrics").text

    UUID(concert["id"])
    UUID(venue["id"])
    UUID(showtime["id"])
    UUID(seats["items"][0]["id"])
    UUID(seat_map["seats"][0]["seatId"])
    assert performances["items"][0]["id"] == showtime["id"]
    assert len(seats["items"]) == 2
    assert public_concert["concertId"] == concert["id"]
    assert public_concert["venue"]["venueId"] == venue["id"]
    assert "performances" not in public_concert
    assert recommended["items"][0]["concertId"] == concert["id"]
    assert recommended["page"]["limit"] == 10
    assert next(day for day in calendar["days"] if day["date"] == f"{starts_at:%Y-%m-%d}")["bookable"] is True
    assert date_performances["performances"][0]["performanceId"] == showtime["id"]
    assert seat_map["sections"][0]["sectionId"] == "A"
    assert len(seat_map["seats"]) == 2
    assert_metric_labels(metrics, "concert_admin_commands_total", command="create_concert", result="success")
    assert_metric_labels(metrics, "seat_inventory_commands_total", command="upload_seat_map", result="success")
    assert_metric_labels(metrics, "catalog_queries_total", resource="concert", result="success")


def test_recommended_concerts_clamps_limit() -> None:
    client = TestClient(create_app())
    for index in range(13):
        create_public_concert(client, title=f"Limit Live {index}")

    response = client.get("/concerts/recommended?limit=99")

    assert response.status_code == 200
    body = response.json()
    assert len(body["items"]) == 12
    assert body["page"]["limit"] == 12
    assert body["page"]["hasMore"] is True
    assert body["page"]["nextCursor"]


def test_new_public_api_validation_errors() -> None:
    client = TestClient(create_app())
    concert, _, _, _ = create_public_concert(client)

    invalid_sort = client.get("/concerts/recommended?sort=popular")
    invalid_limit = client.get("/concerts/recommended?limit=0")
    invalid_month = client.get(f"/concerts/{concert['id']}/calendar?yearMonth=2026-7")

    assert invalid_sort.status_code == 400
    assert invalid_limit.status_code == 422
    assert invalid_month.status_code == 400


def test_date_performances_empty_result() -> None:
    client = TestClient(create_app())
    concert, _, _, _ = create_public_concert(client)

    response = client.get(f"/concerts/{concert['id']}/dates/2099-01-01/performances")

    assert response.status_code == 200
    assert response.json()["performances"] == []


def test_new_public_api_not_found_cases() -> None:
    client = TestClient(create_app())
    missing_concert_id = uuid_id("concert", "missing")
    missing_showtime_id = uuid_id("showtime", "missing")

    detail = client.get(f"/concerts/{missing_concert_id}")
    calendar = client.get(f"/concerts/{missing_concert_id}/calendar?yearMonth=2026-07")
    performances = client.get(f"/concerts/{missing_concert_id}/dates/2026-07-18/performances")
    seat_map = client.get(f"/performances/{missing_showtime_id}/seat-map")

    assert detail.status_code == 404
    assert calendar.status_code == 404
    assert performances.status_code == 404
    assert seat_map.status_code == 404


def test_provider_and_admin_policy_review_flow() -> None:
    client = TestClient(create_app())

    concert = client.post(
        "/provider/concerts",
        json={"title": "Policy Live", "ageRating": "12", "runningMinutes": 90},
    ).json()
    policy = client.put(
        f"/provider/concerts/{concert['id']}/sale-policy",
        json={
            "presaleEnabled": True,
            "fanclubVerificationRequired": False,
            "maxTicketsPerUser": 2,
            "refundPolicy": "Refunds allowed until one day before showtime.",
        },
    ).json()
    approved = client.post(f"/admin/concerts/{concert['id']}/sale-policy/approve", json={}).json()
    scheduled = client.post(
        f"/admin/concerts/{concert['id']}/open-schedule",
        json={"opensAt": (datetime.now(UTC) + timedelta(days=1)).isoformat()},
    ).json()
    metrics = client.get("/metrics").text

    assert policy["status"] == "submitted"
    assert approved["status"] == "approved"
    assert scheduled["status"] == "scheduled"
    assert_metric_labels(metrics, "concert_admin_commands_total", command="update_sale_policy", result="success")
    assert_metric_labels(metrics, "concert_admin_commands_total", command="approve_sale_policy", result="success")


def test_error_response_uses_common_shape() -> None:
    client = TestClient(create_app())

    response = client.get(f"/concerts/{uuid_id('concert', 'missing-error')}", headers={"X-Request-Id": "req-test"})
    metrics = client.get("/metrics").text

    assert response.status_code == 404
    assert response.json()["requestId"] == "req-test"
    assert response.json()["error"]["code"] == "concert.not_found"
    assert_metric_labels(metrics, "catalog_queries_total", resource="concert", result="rejection")


def assert_metric_labels(metrics: str, metric_name: str, **labels: str) -> None:
    label_fragments = [f'{key}="{value}"' for key, value in {"service_name": "concert-service", **labels}.items()]
    assert any(line.startswith(metric_name + "{") and all(fragment in line for fragment in label_fragments) for line in metrics.splitlines())
