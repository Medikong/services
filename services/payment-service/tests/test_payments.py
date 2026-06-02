import os
from pathlib import Path

import pytest

Path("test_payment_service.db").unlink(missing_ok=True)
os.environ["DATABASE_URL"] = "sqlite:///./test_payment_service.db"
os.environ["JWT_SECRET"] = "ticketing-dev-secret"

from fastapi.testclient import TestClient  # noqa: E402

from app.database import Base, SessionLocal, engine  # noqa: E402
import app.main as payment_app  # noqa: E402
from app.main import app  # noqa: E402
from app.models import PaymentEvent  # noqa: E402


client = TestClient(app)


@pytest.fixture(autouse=True)
def reset_db() -> None:
    Base.metadata.drop_all(bind=engine)
    Base.metadata.create_all(bind=engine)


def test_create_and_get_approved_payment(monkeypatch: pytest.MonkeyPatch) -> None:
    published: list[tuple[str, dict]] = []

    async def fake_publish(topic: str, payload: dict) -> bool:
        published.append((topic, payload))
        return True

    monkeypatch.setattr(payment_app.kafka, "publish_event", fake_publish)
    headers = auth_headers("CUSTOMER", user_id="1")
    response = client.post(
        "/payments",
        headers=headers,
        json={
            "reservationId": "res-1",
            "concertId": "concert-1",
            "seatId": "seat-A1",
            "amount": 50000,
            "method": "mock",
            "simulation": "approve",
        },
    )

    assert response.status_code == 201
    payment = response.json()
    assert payment["reservationId"] == "res-1"
    assert payment["concertId"] == "concert-1"
    assert payment["status"] == "approved"
    assert payment["approvedAt"] is not None
    assert payment_events() == ["payment-approved"]
    assert published[0][0] == "payment-approved"
    assert published[0][1]["reservationId"] == "res-1"
    assert published[0][1]["seatId"] == "seat-A1"

    get_response = client.get(f"/payments/{payment['id']}", headers=headers)
    assert get_response.status_code == 200
    assert get_response.json()["id"] == payment["id"]


def test_payment_simulation_fail_and_delay() -> None:
    fail_response = client.post(
        "/payments",
        headers=auth_headers("CUSTOMER", user_id="2"),
        json={
            "reservationId": "res-2",
            "concertId": "concert-1",
            "amount": 50000,
            "method": "mock",
            "simulation": "fail",
        },
    )
    delay_response = client.post(
        "/payments",
        headers=auth_headers("CUSTOMER", user_id="3"),
        json={
            "reservationId": "res-3",
            "concertId": "concert-1",
            "amount": 50000,
            "method": "mock",
            "simulation": "delay",
        },
    )

    assert fail_response.status_code == 201
    assert fail_response.json()["status"] == "failed"
    assert delay_response.status_code == 201
    assert delay_response.json()["status"] == "delayed"
    assert "payment-failed" in payment_events()


def test_idempotency_key_returns_existing_payment() -> None:
    headers = auth_headers("CUSTOMER", user_id="4") | {"Idempotency-Key": "idem-1"}
    payload = {
        "reservationId": "res-4",
        "concertId": "concert-2",
        "amount": 30000,
        "method": "mock",
        "simulation": "approve",
    }

    first = client.post("/payments", headers=headers, json=payload)
    second = client.post("/payments", headers=headers, json=payload)

    assert first.status_code == 201
    assert second.status_code == 201
    assert second.json()["id"] == first.json()["id"]


def test_customer_cannot_read_other_customer_payment() -> None:
    created = client.post(
        "/payments",
        headers=auth_headers("CUSTOMER", user_id="5"),
        json={
            "reservationId": "res-5",
            "concertId": "concert-3",
            "amount": 40000,
            "method": "mock",
        },
    )

    response = client.get(f"/payments/{created.json()['id']}", headers=auth_headers("CUSTOMER", user_id="6"))

    assert response.status_code == 403
    assert response.json()["error"]["code"] == "auth.forbidden"


def test_provider_and_admin_can_get_settlement_basis() -> None:
    client.post(
        "/payments",
        headers=auth_headers("CUSTOMER", user_id="7"),
        json={
            "reservationId": "res-7",
            "concertId": "concert-4",
            "amount": 100000,
            "method": "mock",
        },
    )

    provider_response = client.get(
        "/provider/concerts/concert-4/settlement-basis",
        headers=auth_headers("PROVIDER", user_id="provider-1"),
    )
    admin_response = client.get(
        "/admin/concerts/concert-4/settlement-basis",
        headers=auth_headers("ADMIN", user_id="admin-1"),
    )

    assert provider_response.status_code == 200
    assert provider_response.json()["grossAmount"] == 100000
    assert provider_response.json()["platformFeeAmount"] == 10000
    assert admin_response.status_code == 200


def test_operational_endpoints_and_error_shape() -> None:
    assert client.get("/healthz").status_code == 200
    assert client.get("/readyz").json()["checks"]["database"] == "ok"
    metrics_response = client.get("/metrics")
    assert metrics_response.status_code == 200
    assert metrics_response.headers["content-type"].startswith("text/plain; version=0.0.4")
    assert "http_requests_total" in metrics_response.text
    assert 'service="payment-service"' in metrics_response.text
    assert 'method="GET"' in metrics_response.text
    assert 'path="/healthz"' in metrics_response.text
    assert 'status="200"' in metrics_response.text

    response = client.get("/payments/pay-missing")
    assert response.status_code == 401
    assert response.json()["error"]["code"] == "auth.invalid_token"


def auth_headers(role: str, user_id: str = "1") -> dict[str, str]:
    return {
        "X-User-Id": user_id,
        "X-User-Email": f"{role.lower()}@example.com",
        "X-User-Role": role,
        "X-Token-Id": f"token-{user_id}",
    }


def payment_events() -> list[str]:
    with SessionLocal() as db:
        return [event.event_type for event in db.query(PaymentEvent).order_by(PaymentEvent.created_at).all()]
