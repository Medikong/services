import pytest
from pytest import MonkeyPatch


from fastapi.testclient import TestClient  # noqa: E402

from app.main import app  # noqa: E402
import app.main as app_main  # noqa: E402
from app.database import SessionLocal  # noqa: E402
from app.models import User  # noqa: E402
from app.security import (  # noqa: E402
    UnsupportedPasswordHashError,
    hash_password,
    hash_password_argon2id,
    hash_password_legacy_pbkdf2,
    identify_password_hash,
    verify_password,
)


client = TestClient(app)


def test_lifespan_disposes_engine(monkeypatch: MonkeyPatch) -> None:
    calls: list[str] = []
    monkeypatch.setattr(app_main.engine, "dispose", lambda: calls.append("dispose"))

    with TestClient(app):
        pass

    assert calls == ["dispose"]


def test_signup_creates_customer_and_issues_tokens() -> None:
    response = client.post(
        "/auth/signup",
        json={
            "email": "New.Customer@example.com",
            "password": "newcustomer1234",
            "displayName": " New Customer ",
        },
    )

    assert response.status_code == 201
    body = response.json()
    assert body["accessToken"]
    assert body["refreshToken"]
    assert body["user"]["email"] == "new.customer@example.com"
    assert body["user"]["displayName"] == "New Customer"
    assert body["user"]["role"] == "CUSTOMER"
    assert body["user"]["isActive"] is True

    login_response = client.post(
        "/auth/login",
        json={"email": "new.customer@example.com", "password": "newcustomer1234"},
    )
    assert login_response.status_code == 200
    assert login_response.json()["user"]["role"] == "CUSTOMER"
    with SessionLocal() as db:
        user = db.query(User).filter(User.email == "new.customer@example.com").one()
        assert identify_password_hash(user.password_hash) == "pbkdf2_sha256"


def test_password_hash_supports_argon2id_and_legacy_pbkdf2() -> None:
    default_hash = hash_password("default-password-1234")
    argon2id_hash = hash_password_argon2id("new-password-1234")
    legacy_hash = hash_password_legacy_pbkdf2("legacy-password-1234")

    assert identify_password_hash(default_hash) == "pbkdf2_sha256"
    assert identify_password_hash(argon2id_hash) == "argon2id"
    assert identify_password_hash(legacy_hash) == "pbkdf2_sha256"
    assert verify_password("default-password-1234", default_hash) is True
    assert verify_password("new-password-1234", argon2id_hash) is True
    assert verify_password("wrong-password", argon2id_hash) is False
    assert verify_password("legacy-password-1234", legacy_hash) is True
    assert verify_password("wrong-password", legacy_hash) is False
    with pytest.raises(UnsupportedPasswordHashError):
        verify_password("any-password", "bcrypt$unsupported")


def test_login_accepts_legacy_pbkdf2_password_hash() -> None:
    with SessionLocal() as db:
        db.add(
            User(
                email="legacy.customer@example.com",
                password_hash=hash_password_legacy_pbkdf2("legacycustomer1234"),
                display_name="Legacy Customer",
                role="CUSTOMER",
                is_active=True,
            )
        )
        db.commit()

    response = client.post(
        "/auth/login",
        json={"email": "legacy.customer@example.com", "password": "legacycustomer1234"},
    )

    assert response.status_code == 200
    assert response.json()["user"]["email"] == "legacy.customer@example.com"


def test_signup_rejects_duplicate_email_and_role_field() -> None:
    duplicate_response = client.post(
        "/auth/signup",
        json={
            "email": "customer@example.com",
            "password": "customer1234",
            "displayName": "Duplicate Customer",
        },
    )
    assert duplicate_response.status_code == 409

    role_response = client.post(
        "/auth/signup",
        json={
            "email": "role-customer@example.com",
            "password": "customer1234",
            "displayName": "Role Customer",
            "role": "ADMIN",
        },
    )
    assert role_response.status_code == 422


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


def test_login_records_password_verify_trace_span(monkeypatch: MonkeyPatch) -> None:
    spans: list[tuple[str, dict[str, object]]] = []
    attributes: list[tuple[str, object]] = []

    class FakeSpan:
        def __init__(self, name: str, span_attributes: dict[str, object]) -> None:
            self.name = name
            self.span_attributes = span_attributes

        def __enter__(self) -> None:
            spans.append((self.name, self.span_attributes))
            return None

        def __exit__(self, exc_type: object, exc: object, traceback: object) -> None:
            return None

    class FakeTraceRecorder:
        def span(self, name: str, span_attributes: dict[str, object] | None = None) -> FakeSpan:
            return FakeSpan(name, span_attributes or {})

        def attribute(self, key: str, value: object) -> None:
            attributes.append((key, value))

        def event(self, name: str, event_attributes: dict[str, object] | None = None) -> None:
            return None

    monkeypatch.setattr(app_main, "trace_recorder", lambda: FakeTraceRecorder())

    response = client.post(
        "/auth/login",
        json={"email": "customer@example.com", "password": "customer1234"},
    )

    assert response.status_code == 200
    assert spans == [
        (
            "auth.password.verify",
            {
                "auth.password.scheme": "pbkdf2_sha256",
                "auth.password.iterations": app_main.settings.password_iterations,
            },
        )
    ]
    assert attributes == [("auth.password.valid", True)]


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
