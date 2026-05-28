from datetime import UTC, datetime, timedelta

from fastapi.testclient import TestClient

from app.main import create_app


def test_provider_to_public_concert_flow() -> None:
    client = TestClient(create_app())
    starts_at = datetime.now(UTC) + timedelta(days=7)

    venue = client.post("/provider/venues", json={"name": "Main Hall", "address": "Seoul", "totalSeats": 2}).json()
    concert = client.post(
        "/provider/concerts",
        json={"title": "Spring Live", "ageRating": "ALL", "runningMinutes": 120},
        headers={"X-Provider-Id": "provider-api"},
    ).json()
    showtime = client.post(
        f"/provider/concerts/{concert['id']}/showtimes",
        json={"venueId": venue["id"], "startsAt": starts_at.isoformat()},
    ).json()

    seat_map_response = client.post(
        f"/provider/showtimes/{showtime['id']}/seat-map",
        json={"sections": [{"name": "A", "rows": [{"name": "1", "seatNumbers": ["1", "2"]}]}]},
    )
    performances = client.get(f"/concerts/{concert['id']}/performances").json()
    seats = client.get(f"/performances/{showtime['id']}/seats").json()
    public_concert = client.get(f"/concerts/{concert['id']}").json()

    assert seat_map_response.status_code == 204
    assert performances["items"][0]["id"] == showtime["id"]
    assert len(seats["items"]) == 2
    assert public_concert["venue"]["id"] == venue["id"]


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

    assert policy["status"] == "submitted"
    assert approved["status"] == "approved"
    assert scheduled["status"] == "scheduled"


def test_error_response_uses_common_shape() -> None:
    client = TestClient(create_app())

    response = client.get("/concerts/missing-concert", headers={"X-Request-Id": "req-test"})

    assert response.status_code == 404
    assert response.json()["requestId"] == "req-test"
    assert response.json()["error"]["code"] == "concert.not_found"
