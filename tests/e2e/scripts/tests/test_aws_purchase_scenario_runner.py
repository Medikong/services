# noqa: SIZE_OK - scenario runner coverage is intentionally kept together.
from __future__ import annotations

import json
from pathlib import Path

import pytest

from aws_purchase_scenario_test_support import (
    DROP_ID,
    ORDER_ID,
    PAYMENT_ID,
    PRODUCT_ID,
    RUN_ID,
    SAFE_JWT,
    ScenarioState,
    invoke_runner,
    run_server,
)


def test_dry_run_04_validates_without_http_or_writes(tmp_path: Path) -> None:
    # Given
    state = ScenarioState()

    # When
    with run_server(state) as server:
        invocation = invoke_runner(
            tmp_path,
            base_url=server.base_url,
            mode="dry-run",
        )

    # Then
    assert invocation.result.returncode == 0
    report = invocation.report()
    assert report["verdict"] == "READY"
    assert report["reason_code"] == "DRY_RUN_VERIFIED"
    assert report["purchase_write_requests_sent"] == 0
    assert state.requests == []


@pytest.mark.parametrize("scenario", ["05", "06"])
def test_unsupported_failure_contract_blocks_before_http(
    tmp_path: Path,
    scenario: str,
) -> None:
    # Given
    state = ScenarioState()

    # When
    with run_server(state) as server:
        invocation = invoke_runner(
            tmp_path,
            base_url=server.base_url,
            mode="dry-run",
            scenario=scenario,
        )

    # Then
    assert invocation.result.returncode == 3
    report = invocation.report()
    assert report["verdict"] == "BLOCKED"
    assert report["reason_code"] == "API_CONTRACT_UNSUPPORTED"
    assert "POST /payments/mock-failures" in report["prerequisites"]
    assert report["purchase_write_requests_sent"] == 0
    assert state.requests == []


def test_preflight_uses_public_login_and_bearer_only(tmp_path: Path) -> None:
    # Given
    state = ScenarioState()
    credentials = {
        "SYNTHETIC_CUSTOMER_EMAIL": "customer-a-secret-email",
        "SYNTHETIC_CUSTOMER_PASSWORD": "customer-a-secret-password",
    }

    # When
    with run_server(state) as server:
        invocation = invoke_runner(
            tmp_path,
            base_url=server.base_url,
            mode="preflight",
            runtime_env=credentials,
        )

    # Then
    assert invocation.result.returncode == 0
    report = invocation.report()
    assert report["verdict"] == "READY"
    assert report["reason_code"] == "PREFLIGHT_VERIFIED"
    assert report["purchase_write_requests_sent"] == 0
    assert [request.path for request in state.requests] == [
        "/.well-known/jwks.json",
        "/api/v1/auth/intents",
        "/api/v1/auth/signins/email",
        "/notifications",
        "/notifications",
        f"/drops/{DROP_ID}",
    ]
    assert state.requests[1].header("x-client-channel") == "ios"
    assert state.requests[2].header("x-client-channel") == "ios"
    assert state.requests[3].header("authorization") == ""
    assert state.requests[4].header("authorization") == f"Bearer {SAFE_JWT}"
    for request in state.requests:
        assert not any(name.startswith("x-user-") for name, _ in request.headers)
    emitted = invocation.emitted()
    assert credentials["SYNTHETIC_CUSTOMER_EMAIL"] not in emitted
    assert credentials["SYNTHETIC_CUSTOMER_PASSWORD"] not in emitted
    assert SAFE_JWT not in emitted
    assert server.base_url not in emitted
    assert "private-session-cookie" not in emitted


def test_execute_requires_live_fixture_attestation_before_http(
    tmp_path: Path,
) -> None:
    # Given
    state = ScenarioState()

    # When
    with run_server(state) as server:
        invocation = invoke_runner(
            tmp_path,
            base_url=server.base_url,
            mode="execute",
            extra_args=(
                "--write-opt-in",
                f"aws-dev:{RUN_ID}:ALLOW_PURCHASE_WRITES",
            ),
        )

    # Then
    assert invocation.result.returncode == 3
    assert invocation.report()["reason_code"] == (
        "LIVE_FIXTURE_ATTESTATION_REQUIRED"
    )
    assert invocation.report()["purchase_write_requests_sent"] == 0
    assert state.requests == []


def test_execute_04_is_deterministic_idempotent_and_redacted(
    tmp_path: Path,
) -> None:
    # Given
    state = ScenarioState()
    credentials = {
        "SYNTHETIC_CUSTOMER_EMAIL": "customer-a-secret-email",
        "SYNTHETIC_CUSTOMER_PASSWORD": "customer-a-secret-password",
    }

    # When
    with run_server(state) as server:
        invocation = invoke_runner(
            tmp_path,
            base_url=server.base_url,
            mode="execute",
            runtime_env=credentials,
            live_attestation=True,
        )

    # Then
    assert invocation.result.returncode == 0
    report = invocation.report()
    assert report["verdict"] == "PASS"
    assert report["reason_code"] == "SCENARIO_04_PASSED"
    assert report["purchase_write_requests_sent"] == 4
    order_writes = [
        request
        for request in state.requests
        if request.method == "POST" and request.path == "/orders"
    ]
    payment_writes = [
        request
        for request in state.requests
        if request.method == "POST"
        and request.path == "/payments/mock-approvals"
    ]
    assert len(order_writes) == 2
    assert len(payment_writes) == 2
    assert len({request.header("idempotency-key") for request in order_writes}) == 1
    assert (
        len({request.header("idempotency-key") for request in payment_writes})
        == 1
    )
    assert json.loads(order_writes[0].body) == {
        "dropId": DROP_ID,
        "productId": PRODUCT_ID,
        "quantity": 1,
    }
    for request in (*order_writes, *payment_writes):
        assert request.header("authorization") == f"Bearer {SAFE_JWT}"
        assert not any(name.startswith("x-user-") for name, _ in request.headers)
    emitted = invocation.emitted()
    for sensitive in (
        *credentials.values(),
        SAFE_JWT,
        server.base_url,
        DROP_ID,
        PRODUCT_ID,
        ORDER_ID,
        PAYMENT_ID,
        "private-session-cookie",
    ):
        assert sensitive not in emitted


def test_execute_04_retries_payment_propagation_with_same_idempotency_key(
    tmp_path: Path,
) -> None:
    # Given
    state = ScenarioState(payment_approval_statuses=(409, 201))
    credentials = {
        "SYNTHETIC_CUSTOMER_EMAIL": "customer-a-secret-email",
        "SYNTHETIC_CUSTOMER_PASSWORD": "customer-a-secret-password",
    }

    # When
    with run_server(state) as server:
        invocation = invoke_runner(
            tmp_path,
            base_url=server.base_url,
            mode="execute",
            runtime_env=credentials,
            live_attestation=True,
            extra_args=("--max-attempts", "2"),
        )

    # Then
    assert invocation.result.returncode == 0
    report = invocation.report()
    assert report["verdict"] == "PASS"
    assert report["reason_code"] == "SCENARIO_04_PASSED"
    assert report["purchase_write_requests_sent"] == 5
    assert state.payment_approval_statuses_seen == [409, 201, 201]
    payment_writes = [
        request
        for request in state.requests
        if request.method == "POST"
        and request.path == "/payments/mock-approvals"
    ]
    assert len(payment_writes) == 3
    assert len({request.header("idempotency-key") for request in payment_writes}) == 1
    assert [
        stage["status_code"]
        for stage in report["stages"]
        if stage["name"] in {"payment_approve", "payment_approve_replay"}
    ] == [409, 201, 201]
    emitted = invocation.emitted()
    for sensitive in (
        *credentials.values(),
        SAFE_JWT,
        server.base_url,
        DROP_ID,
        PRODUCT_ID,
        ORDER_ID,
        PAYMENT_ID,
        "private-session-cookie",
    ):
        assert sensitive not in emitted


@pytest.mark.parametrize(
    ("environment", "reason_code"),
    [
        ("", "ENVIRONMENT_REQUIRED"),
        ("default", "ENVIRONMENT_NOT_ALLOWED"),
        ("production", "ENVIRONMENT_NOT_ALLOWED"),
        ("unknown", "ENVIRONMENT_NOT_ALLOWED"),
    ],
)
def test_environment_gate_blocks_without_http(
    tmp_path: Path,
    environment: str,
    reason_code: str,
) -> None:
    # Given
    state = ScenarioState()

    # When
    with run_server(state) as server:
        invocation = invoke_runner(
            tmp_path,
            base_url=server.base_url,
            mode="dry-run",
            extra_args=("--environment", environment),
        )

    # Then
    assert invocation.result.returncode == 3
    assert invocation.report()["reason_code"] == reason_code
    assert state.requests == []


def test_preinjected_jwt_is_refused_before_http(tmp_path: Path) -> None:
    # Given
    state = ScenarioState()
    runtime_env = {
        "AWS_PURCHASE_JWT": SAFE_JWT,
        "SYNTHETIC_CUSTOMER_EMAIL": "customer-a-secret-email",
        "SYNTHETIC_CUSTOMER_PASSWORD": "customer-a-secret-password",
    }

    # When
    with run_server(state) as server:
        invocation = invoke_runner(
            tmp_path,
            base_url=server.base_url,
            mode="preflight",
            runtime_env=runtime_env,
        )

    # Then
    assert invocation.result.returncode == 3
    assert invocation.report()["reason_code"] == "PREINJECTED_TOKEN_FORBIDDEN"
    assert SAFE_JWT not in invocation.emitted()
    assert state.requests == []


def test_stale_evidence_blocks_before_http(tmp_path: Path) -> None:
    # Given
    state = ScenarioState()

    # When
    with run_server(state) as server:
        invocation = invoke_runner(
            tmp_path,
            base_url=server.base_url,
            mode="preflight",
            precreate_output=True,
        )

    # Then
    assert invocation.result.returncode == 3
    assert "OUTPUT_STATE_CONFLICT" in invocation.result.stdout
    assert invocation.output_path.read_text(encoding="utf-8") == "stale"
    assert state.requests == []


def test_direct_service_dns_is_refused_without_network(tmp_path: Path) -> None:
    # Given / When
    invocation = invoke_runner(
        tmp_path,
        base_url="http://order-service.dropmong-order.svc.cluster.local:8082",
        mode="dry-run",
    )

    # Then
    assert invocation.result.returncode == 3
    assert invocation.report()["reason_code"] == (
        "INGRESS_SERVICE_DNS_FORBIDDEN"
    )
    assert invocation.report()["purchase_write_requests_sent"] == 0
