import os
from pathlib import Path

from pytest import MonkeyPatch


Path("test_auth_service.db").unlink(missing_ok=True)
os.environ["DATABASE_URL"] = "sqlite:///./test_auth_service.db"

from fastapi.testclient import TestClient  # noqa: E402

from app.main import app  # noqa: E402
import app.main as app_main  # noqa: E402


client = TestClient(app)


def test_lifespan_disposes_engine(monkeypatch: MonkeyPatch) -> None:
    calls: list[str] = []
    monkeypatch.setattr(app_main.engine, "dispose", lambda: calls.append("dispose"))

    with TestClient(app):
        pass

    assert calls == ["dispose"]


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
    metrics = client.get("/metrics").text
    assert_metric_labels(metrics, "auth_attempts_total", action="login", error_code="none", result="success")
    assert_metric_labels(metrics, "auth_attempts_total", action="me", error_code="none", result="success")
    assert_metric_labels(metrics, "auth_attempts_total", action="logout", error_code="none", result="success")
    assert_metric_labels(metrics, "auth_tokens_issued_total", token_type="access")
    assert_metric_labels(metrics, "auth_token_revocations_total", reason="logout", token_type="access")
    assert_metric_labels(metrics, "audit_events_total", event_type="login_succeeded", outcome="allow")


def test_failed_login_records_auth_rejection_metric() -> None:
    response = client.post(
        "/auth/login",
        json={"email": "admin@example.com", "password": "wrong-password"},
    )

    assert response.status_code == 401
    metrics = client.get("/metrics").text
    assert_metric_labels(metrics, "auth_attempts_total", action="login", error_code="auth.invalid_credentials", result="rejection")


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
    assert metrics_response.headers["content-type"].startswith("text/plain; version=0.0.4")
    assert "http_server_request_duration_seconds" in metrics_response.text
    assert "http_server_active_requests" in metrics_response.text
    assert "service_ready" in metrics_response.text
    assert 'service_name="auth-service"' in metrics_response.text
    assert 'http_request_method="GET"' in metrics_response.text
    assert 'http_route="/healthz"' in metrics_response.text
    assert 'http_response_status_code="200"' in metrics_response.text


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


def assert_metric_labels(metrics: str, metric_name: str, **labels: str) -> None:
    label_fragments = [f'{key}="{value}"' for key, value in {"service_name": "auth-service", **labels}.items()]
    assert any(line.startswith(metric_name + "{") and all(fragment in line for fragment in label_fragments) for line in metrics.splitlines())
