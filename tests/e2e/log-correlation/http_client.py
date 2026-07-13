from __future__ import annotations

import json
import os
import time
from typing import Any
from urllib.error import HTTPError
from urllib.request import Request, urlopen


TIMEOUT_SECONDS = int(os.environ.get("LOG_CORRELATION_TIMEOUT_SECONDS", "120"))


def wait_ready(url: str) -> None:
    deadline = time.monotonic() + TIMEOUT_SECONDS
    while time.monotonic() < deadline:
        try:
            with urlopen(url, timeout=5) as response:
                if response.status == 200:
                    return
        except OSError:
            pass
        time.sleep(2)
    raise AssertionError(f"endpoint did not become ready: {url}")


def request_json(
    method: str,
    url: str,
    *,
    body: dict[str, Any] | None = None,
    request_id: str | None = None,
    headers: dict[str, str] | None = None,
    expected_status: int = 200,
) -> dict[str, Any]:
    request_headers = dict(headers or {})
    if request_id is not None:
        request_headers["X-Request-Id"] = request_id
    data = None
    if body is not None:
        request_headers["Content-Type"] = "application/json"
        data = json.dumps(body).encode("utf-8")
    request = Request(url, data=data, headers=request_headers, method=method)
    try:
        with urlopen(request, timeout=10) as response:
            payload = json.loads(response.read().decode("utf-8"))
            if response.status != expected_status:
                raise AssertionError(f"{method} {url}: expected {expected_status}, got {response.status}")
            return payload
    except HTTPError as error:
        detail = error.read().decode("utf-8", errors="replace")
        raise AssertionError(f"{method} {url}: HTTP {error.code}: {detail}") from error
