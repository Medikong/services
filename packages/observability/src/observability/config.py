from collections.abc import Mapping
from dataclasses import dataclass


DEFAULT_FASTAPI_TRACE_EXCLUDED_URLS = ("/healthz", "/readyz", "/metrics")

OBSERVABILITY_ENV_KEYS = (
    "SERVICE_VERSION",
    "SERVICE_ENVIRONMENT",
    "OTEL_SDK_DISABLED",
    "OTEL_TRACES_EXPORTER",
    "OTEL_EXPORTER_OTLP_ENDPOINT",
    "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
    "OTEL_PYTHON_FASTAPI_EXCLUDED_URLS",
)


@dataclass(frozen=True)
class ObservabilityConfig:
    service_name: str
    service_version: str | None = None
    service_environment: str | None = None
    otel_sdk_disabled: bool = False
    otel_traces_exporter: str = "otlp"
    otlp_trace_exporter_endpoint: str | None = None
    fastapi_trace_excluded_urls: tuple[str, ...] = DEFAULT_FASTAPI_TRACE_EXCLUDED_URLS


def observability_config_from_env(
    service_name: str,
    *,
    env: Mapping[str, str],
) -> ObservabilityConfig:
    otlp_endpoint = _optional_env(env, "OTEL_EXPORTER_OTLP_ENDPOINT")
    otlp_traces_endpoint = _optional_env(env, "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")
    return ObservabilityConfig(
        service_name=service_name,
        service_version=_optional_env(env, "SERVICE_VERSION"),
        service_environment=_optional_env(env, "SERVICE_ENVIRONMENT"),
        otel_sdk_disabled=env.get("OTEL_SDK_DISABLED", "").lower() == "true",
        otel_traces_exporter=env.get("OTEL_TRACES_EXPORTER", "otlp"),
        otlp_trace_exporter_endpoint=otlp_traces_endpoint or otlp_endpoint,
        fastapi_trace_excluded_urls=_fastapi_trace_excluded_urls_from_env(env),
    )


def _optional_env(env: Mapping[str, str], name: str) -> str | None:
    value = env.get(name)
    if value is None or value == "":
        return None
    return value


def _fastapi_trace_excluded_urls_from_env(env: Mapping[str, str]) -> tuple[str, ...]:
    value = env.get("OTEL_PYTHON_FASTAPI_EXCLUDED_URLS")
    if value is None:
        return DEFAULT_FASTAPI_TRACE_EXCLUDED_URLS
    return tuple(part.strip() for part in value.split(",") if part.strip())
