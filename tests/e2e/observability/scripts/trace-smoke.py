from __future__ import annotations

import json
import os
import sys
import time
from dataclasses import dataclass
from typing import Any
from urllib.error import URLError
from urllib.parse import urlencode
from urllib.request import Request, urlopen
from uuid import uuid4


@dataclass(frozen=True)
class Settings:
    service_name: str
    service_url: str
    trace_path: str
    tempo_url: str
    timeout_seconds: int
    sleep_seconds: float
    exclusion_check_seconds: int


def main() -> int:
    settings = Settings(
        service_name=os.getenv("OBS_E2E_SERVICE_NAME", "coupon-service"),
        service_url=os.getenv("OBS_E2E_SERVICE_URL", "http://coupon-service:8080").rstrip("/"),
        trace_path=_trace_path(),
        tempo_url=os.getenv("OBS_E2E_TEMPO_URL", "http://tempo:3200").rstrip("/"),
        timeout_seconds=int(os.getenv("OBS_E2E_TIMEOUT_SECONDS", "90")),
        sleep_seconds=float(os.getenv("OBS_E2E_SLEEP_SECONDS", "2")),
        exclusion_check_seconds=int(os.getenv("OBS_E2E_EXCLUSION_CHECK_SECONDS", "8")),
    )
    request_id = f"obs-e2e-{uuid4()}"
    print(f"observability smoke request_id={request_id} service={settings.service_name}")

    wait_for_json(f"{settings.service_url}/healthz", settings, "service healthz")
    wait_for_json(f"{settings.service_url}/readyz", settings, "service readyz")
    wait_for_http(f"{settings.tempo_url}/ready", settings, "tempo ready")
    wait_for_http(os.getenv("OBS_E2E_COLLECTOR_HEALTH_URL", "http://otel-collector:13133/"), settings, "collector health")

    excluded_request_ids = call_excluded_endpoints(settings)
    setup_coupon_policy(settings)
    call_json(f"{settings.service_url}{settings.trace_path}", headers={"X-Request-Id": request_id})
    deadline = time.monotonic() + settings.timeout_seconds
    last_error = ""
    while time.monotonic() < deadline:
        try:
            trace = find_trace(settings, request_id)
            if trace is not None:
                assert_excluded_endpoint_traces_absent(settings, excluded_request_ids)
                print(
                    "observability smoke passed "
                    f"trace_id={trace.trace_id} span_name={trace.span_name} service={trace.service_name}"
                )
                return 0
        except (OSError, RuntimeError, ValueError) as exc:
            last_error = f"{exc.__class__.__name__}: {exc}"
        time.sleep(settings.sleep_seconds)

    print(
        "observability smoke failed: Tempo에서 요청 trace를 찾지 못했습니다.\n"
        f"- request_id: {request_id}\n"
        f"- service.name: {settings.service_name}\n"
        f"- last_error: {last_error or 'no trace matched'}\n"
        "- 점검 힌트:\n"
        "  docker compose -p dropmong-observability-e2e -f tests/e2e/observability/docker-compose.yml logs otel-collector\n"
        "  docker compose -p dropmong-observability-e2e -f tests/e2e/observability/docker-compose.yml logs tempo\n"
        "  docker compose -p dropmong-observability-e2e -f tests/e2e/observability/docker-compose.yml logs coupon-service",
        file=sys.stderr,
    )
    return 1


@dataclass(frozen=True)
class MatchedTrace:
    trace_id: str
    span_name: str
    service_name: str


def wait_for_json(url: str, settings: Settings, label: str) -> dict[str, Any]:
    deadline = time.monotonic() + settings.timeout_seconds
    last_error = ""
    while time.monotonic() < deadline:
        try:
            return call_json(url)
        except (OSError, RuntimeError, ValueError) as exc:
            last_error = f"{exc.__class__.__name__}: {exc}"
            time.sleep(settings.sleep_seconds)
    raise RuntimeError(f"{label} did not become ready: {last_error}")


def _trace_path() -> str:
    raw_path = os.getenv("OBS_E2E_TRACE_PATH", "/internal/coupon-policies/obs-policy")
    if not raw_path.startswith("/"):
        raise ValueError("OBS_E2E_TRACE_PATH must start with /")
    return raw_path


def setup_coupon_policy(settings: Settings) -> None:
    if os.getenv("OBS_E2E_SETUP_COUPON_POLICY", "true").lower() not in {"1", "true", "yes"}:
        return
    call_json(
        f"{settings.service_url}/internal/coupon-policies",
        method="POST",
        payload={
            "policyId": "obs-policy",
            "dropId": "obs-drop",
            "name": "observability smoke",
            "totalQuantity": 1,
            "status": "ready",
        },
    )


def call_excluded_endpoints(settings: Settings) -> dict[str, str]:
    request_ids = {
        "/healthz": f"obs-e2e-healthz-{uuid4()}",
        "/readyz": f"obs-e2e-readyz-{uuid4()}",
        "/metrics": f"obs-e2e-metrics-{uuid4()}",
    }
    for path, request_id in request_ids.items():
        url = f"{settings.service_url}{path}"
        headers = {"X-Request-Id": request_id}
        if path == "/metrics":
            call_text(url, headers=headers)
        else:
            call_json(url, headers=headers)
    return request_ids


def assert_excluded_endpoint_traces_absent(settings: Settings, request_ids: dict[str, str]) -> None:
    deadline = time.monotonic() + settings.exclusion_check_seconds
    while time.monotonic() < deadline:
        for path, request_id in request_ids.items():
            trace = find_trace(settings, request_id)
            if trace is not None:
                raise RuntimeError(
                    "excluded endpoint trace was found: "
                    f"path={path} request_id={request_id} trace_id={trace.trace_id}"
                )
        time.sleep(settings.sleep_seconds)
    print("observability smoke excluded traces absent paths=/healthz,/readyz,/metrics")


def wait_for_http(url: str, settings: Settings, label: str) -> None:
    deadline = time.monotonic() + settings.timeout_seconds
    last_error = ""
    while time.monotonic() < deadline:
        try:
            call_text(url)
            return
        except (OSError, RuntimeError, ValueError) as exc:
            last_error = f"{exc.__class__.__name__}: {exc}"
            time.sleep(settings.sleep_seconds)
    raise RuntimeError(f"{label} did not become ready: {last_error}")


def find_trace(settings: Settings, request_id: str) -> MatchedTrace | None:
    params = urlencode({"tags": f"service.name={settings.service_name} request_id={request_id}", "limit": "20"})
    search = call_json(f"{settings.tempo_url}/api/search?{params}")
    traces = search.get("traces")
    if not isinstance(traces, list):
        raise RuntimeError(f"unexpected Tempo search response: {search}")

    for item in traces:
        if not isinstance(item, dict):
            continue
        trace_id = item.get("traceID") or item.get("traceId")
        if not isinstance(trace_id, str) or not trace_id:
            continue
        detail = call_json(f"{settings.tempo_url}/api/traces/{trace_id}")
        matched = match_trace_detail(detail, trace_id, settings.service_name, request_id)
        if matched is not None:
            return matched
    return None


def match_trace_detail(
    detail: dict[str, Any],
    trace_id: str,
    expected_service_name: str,
    expected_request_id: str,
) -> MatchedTrace | None:
    for resource_spans in resource_span_items(detail):
        service_name = resource_attribute(resource_spans, "service.name")
        if service_name != expected_service_name:
            continue
        for span in span_items(resource_spans):
            if attribute_value(span.get("attributes"), "request_id") != expected_request_id:
                continue
            span_name = span.get("name")
            if not isinstance(span_name, str) or not span_name:
                raise RuntimeError(f"matched span has no name: trace_id={trace_id}")
            return MatchedTrace(trace_id=trace_id, span_name=span_name, service_name=service_name)
    return None


def resource_span_items(detail: dict[str, Any]) -> list[dict[str, Any]]:
    if isinstance(detail.get("resourceSpans"), list):
        return [item for item in detail["resourceSpans"] if isinstance(item, dict)]
    if isinstance(detail.get("batches"), list):
        return [item for item in detail["batches"] if isinstance(item, dict)]
    return []


def span_items(resource_spans: dict[str, Any]) -> list[dict[str, Any]]:
    scopes = resource_spans.get("scopeSpans")
    if not isinstance(scopes, list):
        scopes = resource_spans.get("instrumentationLibrarySpans")
    if not isinstance(scopes, list):
        return []

    spans: list[dict[str, Any]] = []
    for scope in scopes:
        if not isinstance(scope, dict):
            continue
        scope_spans = scope.get("spans")
        if isinstance(scope_spans, list):
            spans.extend(span for span in scope_spans if isinstance(span, dict))
    return spans


def resource_attribute(resource_spans: dict[str, Any], key: str) -> str | None:
    resource = resource_spans.get("resource")
    if not isinstance(resource, dict):
        return None
    return attribute_value(resource.get("attributes"), key)


def attribute_value(attributes: object, key: str) -> str | None:
    if not isinstance(attributes, list):
        return None
    for attribute in attributes:
        if not isinstance(attribute, dict) or attribute.get("key") != key:
            continue
        value = attribute.get("value")
        if not isinstance(value, dict):
            return None
        return decode_attribute_value(value)
    return None


def decode_attribute_value(value: dict[str, Any]) -> str | None:
    for name in ("stringValue", "intValue", "doubleValue", "boolValue"):
        raw = value.get(name)
        if raw is not None:
            return str(raw)
    return None


def call_json(url: str, headers: dict[str, str] | None = None, method: str = "GET", payload: dict[str, Any] | None = None) -> dict[str, Any]:
    raw = call_text(url, headers=headers, method=method, payload=payload)
    data = json.loads(raw)
    if not isinstance(data, dict):
        raise RuntimeError(f"expected JSON object from {url}")
    return data


def call_text(url: str, headers: dict[str, str] | None = None, method: str = "GET", payload: dict[str, Any] | None = None) -> str:
    data = json.dumps(payload).encode("utf-8") if payload is not None else None
    request_headers = {"Content-Type": "application/json", **(headers or {})}
    request = Request(url, data=data, headers=request_headers, method=method)
    try:
        with urlopen(request, timeout=5) as response:
            if response.status < 200 or response.status >= 300:
                raise RuntimeError(f"{url} returned HTTP {response.status}")
            return response.read().decode("utf-8")
    except URLError as exc:
        raise OSError(f"{url} is not reachable: {exc}") from exc


if __name__ == "__main__":
    raise SystemExit(main())
