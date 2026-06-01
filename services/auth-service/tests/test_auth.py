import os
from pathlib import Path


Path("test_auth_service.db").unlink(missing_ok=True)
os.environ["DATABASE_URL"] = "sqlite:///./test_auth_service.db"

from fastapi.testclient import TestClient  # noqa: E402

from app.main import app  # noqa: E402


client = TestClient(app)


def test_login_me_logout_and_audit_logs() -> None:
    login_response = client.post(
        "/auth/login",
        json={"email": "admin@example.com", "password": "admin1234"},
    )
    assert login_response.status_code == 200
    body = login_response.json()
    token = body["accessToken"]
    refresh_token = body["refreshToken"]
    assert body["user"]["role"] == "ADMIN"
    assert body["expiresIn"] == 900

    headers = {"Authorization": f"Bearer {token}"}
    me_response = client.get("/auth/me", headers=headers)
    assert me_response.status_code == 200
    assert me_response.json()["email"] == "admin@example.com"

    audit_response = client.get("/auth/audit-logs", headers=headers)
    assert audit_response.status_code == 200
    assert any(item["eventType"] == "LOGIN_SUCCEEDED" for item in audit_response.json())

    refresh_response = client.post("/auth/refresh", json={"refreshToken": refresh_token})
    assert refresh_response.status_code == 200
    refreshed = refresh_response.json()
    assert refreshed["accessToken"] != token
    assert refreshed["refreshToken"] != refresh_token

    reuse_response = client.post("/auth/refresh", json={"refreshToken": refresh_token})
    assert reuse_response.status_code == 401

    refreshed_headers = {"Authorization": f"Bearer {refreshed['accessToken']}"}
    logout_response = client.post(
        "/auth/logout",
        headers=refreshed_headers,
        json={"refreshToken": refreshed["refreshToken"]},
    )
    assert logout_response.status_code == 200

    revoked_response = client.get("/auth/me", headers=refreshed_headers)
    assert revoked_response.status_code == 401

    logged_out_refresh_response = client.post("/auth/refresh", json={"refreshToken": refreshed["refreshToken"]})
    assert logged_out_refresh_response.status_code == 401


def test_access_token_contains_ticketing_claim_contract() -> None:
    response = client.post(
        "/auth/login",
        json={"email": "customer@example.com", "password": "customer1234"},
    )

    assert response.status_code == 200
    token = response.json()["accessToken"]
    _header, payload, _signature = token.split(".")
    import base64
    import json

    padded = payload + "=" * (-len(payload) % 4)
    claims = json.loads(base64.urlsafe_b64decode(padded.encode("ascii")))
    assert claims["iss"] == "auth-service"
    assert claims["email"] == "customer@example.com"
    assert claims["role"] == "CUSTOMER"
    assert {"iss", "sub", "email", "role", "iat", "exp", "jti"} <= set(claims)


def test_operational_endpoints() -> None:
    health_response = client.get("/healthz")
    assert health_response.status_code == 200
    assert health_response.json()["status"] == "ok"
    assert health_response.json()["service"] == "auth-service"

    ready_response = client.get("/readyz")
    assert ready_response.status_code == 200
    assert ready_response.json()["status"] == "ready"
    assert ready_response.json()["checks"]["database"] == "ok"

    metrics_response = client.get("/metrics")
    assert metrics_response.status_code == 200
    assert "http_requests_total" in metrics_response.text


def test_error_response_uses_common_shape() -> None:
    response = client.get("/auth/me")

    assert response.status_code == 401
    body = response.json()
    assert body["error"]["code"] == "auth.invalid_token"
    assert body["requestId"]
    assert body["occurredAt"]


def test_demo_accounts_are_exposed_for_frontend_login_shortcuts() -> None:
    response = client.get("/auth/demo-accounts")

    assert response.status_code == 200
    body = response.json()
    assert {account["role"] for account in body} == {"CUSTOMER", "PROVIDER", "ADMIN"}
    assert any(account["email"] == "customer@example.com" for account in body)
