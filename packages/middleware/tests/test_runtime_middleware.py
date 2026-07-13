from __future__ import annotations

import asyncio
import json
from concurrent.futures import CancelledError as FutureCancelledError
from dataclasses import dataclass
from typing import Any

import pytest

from middleware import (
    CLIENT_ACTION_ID_HEADER,
    REQUEST_ID_HEADER,
    RequestContextMiddleware,
    ResponseHeadersMiddleware,
    RuntimeRecoveryMiddleware,
    get_current_client_action_id,
    get_current_request_context,
    get_current_request_id,
)
from middleware.types import ASGIApp, Message, Receive, Scope, Send


def test_request_context_stores_request_and_client_action_ids() -> None:
    messages = _call(_runtime_stack(_context_echo_app), headers={"X-Request-Id": "req-1", "X-Client-Action-Id": "act-1"})

    response = _response(messages)

    assert response.status == 200
    assert response.headers["x-request-id"] == "req-1"
    assert response.body == {
        "requestId": "req-1",
        "clientActionId": "act-1",
        "stateRequestId": "req-1",
        "stateClientActionId": "act-1",
    }
    assert get_current_request_context() is None


def test_request_context_generates_request_id_when_header_is_missing() -> None:
    messages = _call(_runtime_stack(_context_echo_app))

    response = _response(messages)

    assert response.status == 200
    assert response.body["requestId"]
    assert response.body["requestId"] == response.headers["x-request-id"]
    assert response.body["clientActionId"] is None


@pytest.mark.parametrize(
    "unsafe_request_id",
    (
        "4111111111111111",
        "Bearer.secret.value",
        "eyJhbGciOiJIUzI1NiJ9.payload.signature",
        "token-super-secret",
        "x" * 8192,
    ),
)
def test_request_context_replaces_sensitive_request_id(unsafe_request_id: str) -> None:
    messages = _call(_runtime_stack(_context_echo_app), headers={"X-Request-Id": unsafe_request_id})

    response = _response(messages)

    assert response.body["requestId"] != unsafe_request_id
    assert response.body["requestId"] == response.headers["x-request-id"]


def test_runtime_recovery_returns_500_for_unhandled_exception() -> None:
    async def failing_app(scope: Scope, receive: Receive, send: Send) -> None:
        raise RuntimeError("boom")

    messages = _call(_runtime_stack(failing_app), headers={"X-Request-Id": "req-boom"})

    response = _response(messages)

    assert response.status == 500
    assert response.headers["x-request-id"] == "req-boom"
    assert response.body == {
        "status": "error",
        "error": "internal_server_error",
        "requestId": "req-boom",
    }
    assert get_current_request_id() is None
    assert get_current_client_action_id() is None


def test_runtime_recovery_does_not_catch_cancellation() -> None:
    async def cancelled_app(scope: Scope, receive: Receive, send: Send) -> None:
        raise FutureCancelledError()

    with pytest.raises(FutureCancelledError):
        _call(_runtime_stack(cancelled_app))


def test_runtime_recovery_does_not_catch_base_exception() -> None:
    async def interrupted_app(scope: Scope, receive: Receive, send: Send) -> None:
        raise KeyboardInterrupt()

    with pytest.raises(KeyboardInterrupt):
        _call(_runtime_stack(interrupted_app))


def test_runtime_recovery_reraises_after_response_started() -> None:
    async def failing_after_start(scope: Scope, receive: Receive, send: Send) -> None:
        await send({"type": "http.response.start", "status": 200, "headers": []})
        raise RuntimeError("late failure")

    with pytest.raises(RuntimeError):
        _call(_runtime_stack(failing_after_start))


async def _context_echo_app(scope: Scope, receive: Receive, send: Send) -> None:
    body = json.dumps(
        {
            "requestId": get_current_request_id(),
            "clientActionId": get_current_client_action_id(),
            "stateRequestId": scope["state"]["request_id"],
            "stateClientActionId": scope["state"]["client_action_id"],
        }
    ).encode("utf-8")
    await send(
        {
            "type": "http.response.start",
            "status": 200,
            "headers": [(b"content-type", b"application/json"), (b"content-length", str(len(body)).encode("latin-1"))],
        }
    )
    await send({"type": "http.response.body", "body": body})


def _runtime_stack(app: ASGIApp) -> ASGIApp:
    return RequestContextMiddleware(ResponseHeadersMiddleware(RuntimeRecoveryMiddleware(app)))


def _call(app: ASGIApp, *, headers: dict[str, str] | None = None) -> list[Message]:
    async def run() -> list[Message]:
        request_sent = False
        messages: list[Message] = []
        scope: Scope = {
            "type": "http",
            "asgi": {"version": "3.0"},
            "http_version": "1.1",
            "method": "GET",
            "scheme": "http",
            "path": "/",
            "raw_path": b"/",
            "query_string": b"",
            "headers": [
                (name.lower().encode("latin-1"), value.encode("latin-1")) for name, value in (headers or {}).items()
            ],
            "client": ("127.0.0.1", 12345),
            "server": ("testserver", 80),
        }

        async def receive() -> Message:
            nonlocal request_sent
            if not request_sent:
                request_sent = True
                return {"type": "http.request", "body": b"", "more_body": False}
            return {"type": "http.disconnect"}

        async def send(message: Message) -> None:
            messages.append(message)

        await app(scope, receive, send)
        return messages

    return asyncio.run(run())


@dataclass(frozen=True)
class ResponseRecord:
    status: int
    headers: dict[str, str]
    body: Any


def _response(messages: list[Message]) -> ResponseRecord:
    start = next(message for message in messages if message["type"] == "http.response.start")
    body_message = next(message for message in messages if message["type"] == "http.response.body")
    headers = {name.decode("latin-1"): value.decode("latin-1") for name, value in start["headers"]}
    return ResponseRecord(
        status=start["status"],
        headers=headers,
        body=json.loads(body_message.get("body", b"{}").decode("utf-8")),
    )
