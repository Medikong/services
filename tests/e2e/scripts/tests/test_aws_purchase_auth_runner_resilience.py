from __future__ import annotations

import socket
import time
from pathlib import Path

from aws_purchase_auth_test_support import (
    SAFE_JWT,
    ScenarioState,
    invoke_runner,
    run_server,
)


def test_transient_auth_route_failures_use_bounded_retry(tmp_path: Path) -> None:
    state = ScenarioState(auth_statuses=(503, 503, 200))

    with run_server(state) as server:
        invocation = invoke_runner(
            tmp_path,
            base_url=server.base_url,
            extra_args=("--max-attempts", "3"),
        )

    assert invocation.result.returncode == 0
    auth_calls = [
        request
        for request in state.requests
        if request.path == "/.well-known/jwks.json"
    ]
    assert len(auth_calls) == 3
    auth_stage = invocation.report()["stages"][0]
    assert auth_stage["attempts"] == 3


def test_unreachable_ingress_stops_after_configured_attempts(tmp_path: Path) -> None:
    with socket.socket() as probe:
        probe.bind(("127.0.0.1", 0))
        _, port = probe.getsockname()
    started = time.monotonic()

    invocation = invoke_runner(
        tmp_path,
        base_url=f"http://127.0.0.1:{port}",
        extra_args=("--max-attempts", "2", "--timeout-seconds", "0.2"),
    )

    assert time.monotonic() - started < 5
    assert invocation.result.returncode == 3
    assert invocation.report()["reason_code"] == "INGRESS_UNREACHABLE"
    assert invocation.report()["stages"][0]["attempts"] == 2


def test_stale_json_output_blocks_before_network(tmp_path: Path) -> None:
    state = ScenarioState()

    with run_server(state) as server:
        invocation = invoke_runner(
            tmp_path,
            base_url=server.base_url,
            precreate_json=True,
        )

    assert invocation.result.returncode == 3
    assert "OUTPUT_STATE_CONFLICT" in invocation.result.stdout
    assert state.requests == []
    assert invocation.json_path.read_text(encoding="utf-8") == "stale"


def test_redirected_auth_route_does_not_follow_another_base_url(
    tmp_path: Path,
) -> None:
    state = ScenarioState(
        auth_statuses=(302,),
        redirect_location="http://unapproved.example.invalid/jwks",
    )

    with run_server(state) as server:
        invocation = invoke_runner(tmp_path, base_url=server.base_url)

    assert invocation.result.returncode == 3
    assert invocation.report()["reason_code"] == "AUTH_ROUTE_UNRESOLVED"
    assert len(state.requests) == 1


def test_interrupted_connections_are_bounded_and_redacted(tmp_path: Path) -> None:
    state = ScenarioState(dropped_auth_connections=2)

    with run_server(state) as server:
        invocation = invoke_runner(
            tmp_path,
            base_url=server.base_url,
            extra_args=("--max-attempts", "2"),
        )

    assert invocation.result.returncode == 3
    assert invocation.report()["reason_code"] == "INGRESS_UNREACHABLE"
    assert len(state.requests) == 2
    assert SAFE_JWT not in invocation.emitted()


def test_absolute_auth_path_is_rejected_before_network(tmp_path: Path) -> None:
    state = ScenarioState()

    with run_server(state) as server:
        invocation = invoke_runner(
            tmp_path,
            base_url=server.base_url,
            extra_args=("--auth-route", "http://other.example/jwks"),
        )

    assert invocation.result.returncode == 3
    assert invocation.report()["reason_code"] == "ROUTE_PATH_INVALID"
    assert state.requests == []
