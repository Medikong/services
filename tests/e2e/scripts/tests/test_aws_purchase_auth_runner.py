from __future__ import annotations

import json
from pathlib import Path
from xml.etree import ElementTree

import pytest

from aws_purchase_auth_test_support import (
    RUN_ID,
    SAFE_JWT,
    ScenarioState,
    invoke_runner,
    run_server,
)


def test_verified_token_reaches_protected_route_without_identity_headers(
    tmp_path: Path,
) -> None:
    state = ScenarioState(response_marker="session-cookie-value")

    with run_server(state) as server:
        invocation = invoke_runner(tmp_path, base_url=server.base_url)

    assert invocation.result.returncode == 0
    report = invocation.report()
    assert report["verdict"] == "VERIFIED"
    assert report["purchase_traffic_allowed"] is True
    assert report["purchase_requests_sent"] == 0
    assert [request.path for request in state.requests] == [
        "/.well-known/jwks.json",
        "/api/v1/users/me",
        "/api/v1/users/me",
    ]
    assert state.requests[1].header("authorization") == ""
    assert state.requests[2].header("authorization") == f"Bearer {SAFE_JWT}"
    for request in state.requests:
        assert not any(name.startswith("x-user-") for name, _ in request.headers)
        assert request.header("x-request-id").startswith(RUN_ID)
        assert request.header("idempotency-key").startswith(RUN_ID)
    emitted = invocation.emitted()
    assert SAFE_JWT not in emitted
    assert "session-cookie-value" not in emitted
    assert server.base_url not in emitted
    root = ElementTree.parse(invocation.junit_path).getroot()
    assert root.attrib["failures"] == "0"
    assert root.attrib["skipped"] == "0"


def test_missing_credentials_block_before_any_http_request(tmp_path: Path) -> None:
    state = ScenarioState()

    with run_server(state) as server:
        invocation = invoke_runner(tmp_path, base_url=server.base_url, runtime_env={})

    assert invocation.result.returncode == 3
    assert invocation.report()["reason_code"] == "CREDENTIALS_MISSING"
    assert state.requests == []


def test_ingress_url_can_come_from_runtime_environment(tmp_path: Path) -> None:
    state = ScenarioState()

    with run_server(state) as server:
        invocation = invoke_runner(
            tmp_path,
            base_url=None,
            runtime_env={
                "AWS_PURCHASE_INGRESS_BASE_URL": server.base_url,
                "AWS_PURCHASE_JWT": SAFE_JWT,
            },
        )

    assert invocation.result.returncode == 0
    assert invocation.report()["purchase_requests_sent"] == 0


def test_missing_ingress_url_blocks_without_a_network_fallback(
    tmp_path: Path,
) -> None:
    invocation = invoke_runner(
        tmp_path,
        base_url=None,
        runtime_env={"AWS_PURCHASE_JWT": SAFE_JWT},
    )

    assert invocation.result.returncode == 3
    assert invocation.report()["reason_code"] == "INGRESS_URL_MISSING"
    assert invocation.report()["purchase_requests_sent"] == 0


def test_malformed_token_blocks_before_any_http_request(tmp_path: Path) -> None:
    state = ScenarioState()

    with run_server(state) as server:
        invocation = invoke_runner(
            tmp_path,
            base_url=server.base_url,
            runtime_env={"AWS_PURCHASE_JWT": "not-a-jwt"},
        )

    assert invocation.result.returncode == 3
    assert invocation.report()["reason_code"] == "TOKEN_INVALID_FORMAT"
    assert state.requests == []


def test_unresolved_public_auth_route_blocks_protected_calls(tmp_path: Path) -> None:
    state = ScenarioState(auth_statuses=(404,))

    with run_server(state) as server:
        invocation = invoke_runner(tmp_path, base_url=server.base_url)

    assert invocation.result.returncode == 3
    assert invocation.report()["reason_code"] == "AUTH_ROUTE_UNRESOLVED"
    assert [request.path for request in state.requests] == ["/.well-known/jwks.json"]


def test_open_anonymous_protected_route_fails_before_bearer_call(
    tmp_path: Path,
) -> None:
    state = ScenarioState(anonymous_status=200)

    with run_server(state) as server:
        invocation = invoke_runner(tmp_path, base_url=server.base_url)

    assert invocation.result.returncode == 2
    assert invocation.report()["reason_code"] == "PROTECTED_ROUTE_OPEN"
    protected = [
        request for request in state.requests if request.path == "/api/v1/users/me"
    ]
    assert len(protected) == 1
    assert protected[0].header("authorization") == ""


def test_rejected_bearer_token_is_classified_without_leaking_it(
    tmp_path: Path,
) -> None:
    state = ScenarioState(authenticated_status=401)

    with run_server(state) as server:
        invocation = invoke_runner(tmp_path, base_url=server.base_url)

    assert invocation.result.returncode == 2
    assert invocation.report()["reason_code"] == "TOKEN_REJECTED"
    assert SAFE_JWT not in invocation.emitted()


@pytest.mark.parametrize(
    ("base_url", "reason_code"),
    [
        ("not-a-url", "INGRESS_URL_INVALID"),
    ],
)
def test_invalid_ingress_configuration_blocks_without_fallback(
    tmp_path: Path,
    base_url: str,
    reason_code: str,
) -> None:
    invocation = invoke_runner(tmp_path, base_url=base_url)

    assert invocation.result.returncode == 3
    assert invocation.report()["reason_code"] == reason_code


@pytest.mark.parametrize(
    "base_url",
    [
        "http://auth-service:8081",
        "http://auth-service.default:8081",
        "http://auth-service.default.svc:8081",
        "http://auth-service.default.svc.cluster.local:8081",
    ],
)
def test_direct_kubernetes_service_dns_blocks_before_network(
    tmp_path: Path,
    base_url: str,
) -> None:
    invocation = invoke_runner(tmp_path, base_url=base_url)

    assert invocation.result.returncode == 3
    report = invocation.report()
    assert report["reason_code"] == "INGRESS_SERVICE_DNS_FORBIDDEN"
    assert report["stages"] == []
    assert report["purchase_requests_sent"] == 0


@pytest.mark.parametrize(
    ("expected_fingerprint", "reason_code"),
    [
        ("", "INGRESS_IDENTITY_MISSING"),
        ("sha256:invalid", "INGRESS_IDENTITY_INVALID"),
        (f"sha256:{'0' * 64}", "INGRESS_IDENTITY_MISMATCH"),
    ],
)
def test_unapproved_ingress_identity_blocks_before_network(
    tmp_path: Path,
    expected_fingerprint: str,
    reason_code: str,
) -> None:
    invocation = invoke_runner(
        tmp_path,
        base_url="http://private-service.private-lan:8081",
        runtime_env={
            "AWS_PURCHASE_EXPECTED_INGRESS_FINGERPRINT": expected_fingerprint,
            "AWS_PURCHASE_JWT": SAFE_JWT,
        },
    )

    assert invocation.result.returncode == 3
    report = invocation.report()
    assert report["reason_code"] == reason_code
    assert report["stages"] == []
    assert report["purchase_requests_sent"] == 0


def test_mobile_login_acquires_jwt_from_secret_environment_pair(
    tmp_path: Path,
) -> None:
    state = ScenarioState()
    runtime_env = {
        "SYNTHETIC_CUSTOMER_EMAIL": "synthetic-customer",
        "SYNTHETIC_CUSTOMER_PASSWORD": "credential-value",
    }

    with run_server(state) as server:
        invocation = invoke_runner(
            tmp_path,
            base_url=server.base_url,
            runtime_env=runtime_env,
        )

    assert invocation.result.returncode == 0
    paths = [request.path for request in state.requests]
    assert paths[:3] == [
        "/.well-known/jwks.json",
        "/api/v1/auth/intents",
        "/api/v1/auth/signins/email",
    ]
    sign_in = state.requests[2]
    assert sign_in.header("x-auth-flow-token") == "flow-token"
    assert json.loads(sign_in.body)["email"] == runtime_env["SYNTHETIC_CUSTOMER_EMAIL"]
    emitted = invocation.emitted()
    assert runtime_env["SYNTHETIC_CUSTOMER_EMAIL"] not in emitted
    assert runtime_env["SYNTHETIC_CUSTOMER_PASSWORD"] not in emitted
    assert SAFE_JWT not in emitted


def test_login_response_without_access_token_fails_before_protected_call(
    tmp_path: Path,
) -> None:
    state = ScenarioState(login_token=None)

    with run_server(state) as server:
        invocation = invoke_runner(
            tmp_path,
            base_url=server.base_url,
            runtime_env={
                "SYNTHETIC_CUSTOMER_EMAIL": "synthetic-customer",
                "SYNTHETIC_CUSTOMER_PASSWORD": "credential-value",
            },
        )

    assert invocation.result.returncode == 2
    assert invocation.report()["reason_code"] == "TOKEN_MISSING"
    assert not any(request.path == "/api/v1/users/me" for request in state.requests)
