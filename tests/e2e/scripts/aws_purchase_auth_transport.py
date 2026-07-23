from __future__ import annotations

import json
import time
from collections.abc import Sequence
from dataclasses import dataclass
from types import TracebackType
from typing import cast

import httpx2

from aws_purchase_auth_contract import JsonValue, RunnerConfig


@dataclass(frozen=True, slots=True)
class RequestReceipt:
    response: httpx2.Response
    attempts: int
    request_id: str
    idempotency_key: str


class IngressUnreachable(Exception):
    def __init__(
        self,
        *,
        attempts: int,
        request_id: str,
        idempotency_key: str,
    ) -> None:
        super().__init__("INGRESS_UNREACHABLE")
        self.attempts = attempts
        self.request_id = request_id
        self.idempotency_key = idempotency_key


class BoundedHttpClient:
    def __init__(self, config: RunnerConfig) -> None:
        self._config = config
        self._client = httpx2.Client(
            base_url=config.base_url,
            http2=True,
            follow_redirects=False,
            timeout=config.timeout_seconds,
            trust_env=False,
            headers={"Accept": "application/json"},
        )

    def __enter__(self) -> BoundedHttpClient:
        self._client.__enter__()
        return self

    def __exit__(
        self,
        exc_type: type[BaseException] | None,
        exc_value: BaseException | None,
        traceback: TracebackType | None,
    ) -> None:
        self._client.__exit__(exc_type, exc_value, traceback)

    def request(
        self,
        stage: str,
        method: str,
        path: str,
        *,
        payload: dict[str, JsonValue] | None = None,
        headers: dict[str, str] | None = None,
    ) -> RequestReceipt:
        idempotency_key = f"{self._config.run_id}-{stage}"
        for attempt in range(1, self._config.max_attempts + 1):
            request_id = f"{idempotency_key}-{attempt}"
            request_headers = {
                "X-Request-Id": request_id,
                "Idempotency-Key": idempotency_key,
                **(headers or {}),
            }
            try:
                response = self._client.request(
                    method,
                    path,
                    headers=request_headers,
                    json=payload,
                )
            except httpx2.TransportError:
                if attempt == self._config.max_attempts:
                    raise IngressUnreachable(
                        attempts=attempt,
                        request_id=request_id,
                        idempotency_key=idempotency_key,
                    ) from None
                self._backoff(attempt)
                continue
            if response.status_code < 500 or attempt == self._config.max_attempts:
                return RequestReceipt(
                    response=response,
                    attempts=attempt,
                    request_id=request_id,
                    idempotency_key=idempotency_key,
                )
            self._backoff(attempt)
        raise AssertionError("bounded request loop exhausted")

    def _backoff(self, attempt: int) -> None:
        delay = min(self._config.backoff_seconds * (2 ** (attempt - 1)), 2)
        if delay > 0:
            time.sleep(delay)


def decode_json(response: httpx2.Response) -> JsonValue | None:
    try:
        decoded = cast("JsonValue", json.loads(response.content))
    except (json.JSONDecodeError, UnicodeDecodeError):
        return None
    return decoded


def json_member(value: JsonValue | None, path: Sequence[str]) -> JsonValue | None:
    current = value
    for name in path:
        if not isinstance(current, dict):
            return None
        current = current.get(name)
    return current


def first_string(*values: JsonValue | None) -> str | None:
    for value in values:
        if isinstance(value, str) and value:
            return value
    return None
