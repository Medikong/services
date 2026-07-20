from collections.abc import Mapping
from dataclasses import dataclass


DEFAULT_FASTAPI_TRACE_EXCLUDED_URLS = ("/healthz", "/readyz", "/metrics")
DEFAULT_CALLSITE_MODULE_PREFIXES = ("app",)
DEFAULT_PYROSCOPE_SAMPLE_RATE = 100
PYROSCOPE_ALLOWED_TAG_KEYS = ("environment", "run_id", "scenario", "service", "version")
PYROSCOPE_FORBIDDEN_TAG_KEYS = ("payment_id", "reservation_id", "ticket_id", "user_id")
CALLSITE_MODULE_PREFIXES_ENV = "OBSERVABILITY_CALLSITE_MODULE_PREFIXES"

OBSERVABILITY_ENV_KEYS = (
    "SERVICE_VERSION",
    "SERVICE_ENVIRONMENT",
    "OTEL_SDK_DISABLED",
    "OTEL_TRACES_EXPORTER",
    "OTEL_EXPORTER_OTLP_ENDPOINT",
    "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
    "OTEL_PYTHON_FASTAPI_EXCLUDED_URLS",
    CALLSITE_MODULE_PREFIXES_ENV,
    "PYROSCOPE_ENABLED",
    "PYROSCOPE_SERVER_ADDRESS",
    "PYROSCOPE_APPLICATION_NAME",
    "PYROSCOPE_SAMPLE_RATE",
    "PYROSCOPE_SPAN_PROFILES_ENABLED",
    "PYROSCOPE_ONCPU",
    "PYROSCOPE_GIL_ONLY",
    "PYROSCOPE_TAGS",
    "PYROSCOPE_BASIC_AUTH_USERNAME",
    "PYROSCOPE_BASIC_AUTH_PASSWORD",
    "PYROSCOPE_TENANT_ID",
)


@dataclass(frozen=True)
class ProfilingConfig:
    enabled: bool = False
    server_address: str | None = None
    application_name: str | None = None
    sample_rate: int = DEFAULT_PYROSCOPE_SAMPLE_RATE
    span_profiles_enabled: bool = False
    oncpu: bool = True
    gil_only: bool = True
    tags: Mapping[str, str] | None = None
    basic_auth_username: str | None = None
    basic_auth_password: str | None = None
    tenant_id: str | None = None


@dataclass(frozen=True)
class ObservabilityConfig:
    service_name: str
    service_version: str | None = None
    service_environment: str | None = None
    otel_sdk_disabled: bool = False
    otel_traces_exporter: str = "otlp"
    otlp_trace_exporter_endpoint: str | None = None
    fastapi_trace_excluded_urls: tuple[str, ...] = DEFAULT_FASTAPI_TRACE_EXCLUDED_URLS
    callsite_module_prefixes: tuple[str, ...] = DEFAULT_CALLSITE_MODULE_PREFIXES
    profiling: ProfilingConfig = ProfilingConfig()


def observability_config_from_env(
    service_name: str,
    *,
    env: Mapping[str, str],
    default_service_version: str | None = None,
    default_service_environment: str | None = None,
) -> ObservabilityConfig:
    otlp_endpoint = _optional_env(env, "OTEL_EXPORTER_OTLP_ENDPOINT")
    otlp_traces_endpoint = _optional_env(env, "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")
    return ObservabilityConfig(
        service_name=service_name,
        service_version=_optional_env(env, "SERVICE_VERSION")
        or default_service_version,
        service_environment=(
            _optional_env(env, "SERVICE_ENVIRONMENT") or default_service_environment
        ),
        otel_sdk_disabled=env.get("OTEL_SDK_DISABLED", "").lower() == "true",
        otel_traces_exporter=env.get("OTEL_TRACES_EXPORTER", "otlp"),
        otlp_trace_exporter_endpoint=otlp_traces_endpoint or otlp_endpoint,
        fastapi_trace_excluded_urls=_fastapi_trace_excluded_urls_from_env(env),
        callsite_module_prefixes=_callsite_module_prefixes_from_env(env),
        profiling=_profiling_config_from_env(service_name, env),
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


def _callsite_module_prefixes_from_env(env: Mapping[str, str]) -> tuple[str, ...]:
    value = env.get(CALLSITE_MODULE_PREFIXES_ENV)
    if value is None:
        return DEFAULT_CALLSITE_MODULE_PREFIXES
    prefixes = tuple(part.strip() for part in value.split(",") if part.strip())
    return prefixes or DEFAULT_CALLSITE_MODULE_PREFIXES


def _profiling_config_from_env(
    service_name: str, env: Mapping[str, str]
) -> ProfilingConfig:
    tags = _profiling_tags_from_env(
        env,
        service_name=service_name,
        service_version=_optional_env(env, "SERVICE_VERSION"),
        service_environment=_optional_env(env, "SERVICE_ENVIRONMENT"),
    )
    return ProfilingConfig(
        enabled=_bool_env(env, "PYROSCOPE_ENABLED", default=False),
        server_address=_optional_env(env, "PYROSCOPE_SERVER_ADDRESS"),
        application_name=_optional_env(env, "PYROSCOPE_APPLICATION_NAME")
        or service_name,
        sample_rate=_positive_int_env(
            env, "PYROSCOPE_SAMPLE_RATE", default=DEFAULT_PYROSCOPE_SAMPLE_RATE
        ),
        span_profiles_enabled=_bool_env(
            env, "PYROSCOPE_SPAN_PROFILES_ENABLED", default=False
        ),
        oncpu=_bool_env(env, "PYROSCOPE_ONCPU", default=True),
        gil_only=_bool_env(env, "PYROSCOPE_GIL_ONLY", default=True),
        tags=tags,
        basic_auth_username=_optional_env(env, "PYROSCOPE_BASIC_AUTH_USERNAME"),
        basic_auth_password=_optional_env(env, "PYROSCOPE_BASIC_AUTH_PASSWORD"),
        tenant_id=_optional_env(env, "PYROSCOPE_TENANT_ID"),
    )


def _profiling_tags_from_env(
    env: Mapping[str, str],
    *,
    service_name: str,
    service_version: str | None,
    service_environment: str | None,
) -> dict[str, str]:
    tags = {"service": service_name}
    if service_environment:
        tags["environment"] = service_environment
    if service_version:
        tags["version"] = service_version
    for key, value in _parse_tag_env(env.get("PYROSCOPE_TAGS")).items():
        normalized_key = _validate_pyroscope_tag_key(key)
        tags[normalized_key] = value
    return tags


def _parse_tag_env(value: str | None) -> dict[str, str]:
    if value is None or value.strip() == "":
        return {}
    tags: dict[str, str] = {}
    for part in value.split(","):
        tag = part.strip()
        if not tag:
            continue
        key, separator, tag_value = tag.partition("=")
        key = key.strip()
        tag_value = tag_value.strip()
        if not separator or not key or not tag_value:
            raise ValueError(
                "PYROSCOPE_TAGS entries must use non-empty key=value pairs"
            )
        tags[key] = tag_value
    return tags


def _validate_pyroscope_tag_key(key: str) -> str:
    normalized = key.strip().lower()
    if (
        normalized in PYROSCOPE_FORBIDDEN_TAG_KEYS
        or normalized.endswith("_id")
        and normalized != "run_id"
    ):
        raise ValueError(
            f"PYROSCOPE_TAGS contains forbidden high-cardinality tag key: {key}"
        )
    if normalized not in PYROSCOPE_ALLOWED_TAG_KEYS:
        allowed = ", ".join(PYROSCOPE_ALLOWED_TAG_KEYS)
        raise ValueError(f"PYROSCOPE_TAGS allows only low-cardinality keys: {allowed}")
    return normalized


def _bool_env(env: Mapping[str, str], name: str, *, default: bool) -> bool:
    value = env.get(name)
    if value is None or value == "":
        return default
    return value.strip().lower() == "true"


def _positive_int_env(env: Mapping[str, str], name: str, *, default: int) -> int:
    value = env.get(name)
    if value is None or value == "":
        return default
    try:
        parsed = int(value)
    except ValueError as exc:
        raise ValueError(f"{name} must be a positive integer") from exc
    if parsed <= 0:
        raise ValueError(f"{name} must be a positive integer")
    return parsed
