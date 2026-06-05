from opentelemetry import trace
from opentelemetry.sdk.resources import DEPLOYMENT_ENVIRONMENT, SERVICE_NAME, SERVICE_VERSION, Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor

from observability.config import ObservabilityConfig


_tracing_configured = False


def configure_process_tracing(config: ObservabilityConfig) -> None:
    """서비스 시작 시 프로세스 전체 OpenTelemetry tracer provider를 설정한다.

    이 함수는 OpenTelemetry provider registry를 바꾸므로 전역 부작용이 있다.
    의존성으로 주입해 쓰는 객체가 아니라, 앱 시작 단계에서 한 번 붙이는 배선으로 본다.
    런타임 선택은 ObservabilityConfig로만 받아 env 해석 지점을 한 곳에 묶어 둔다.
    """
    global _tracing_configured

    # Tracer provider는 프로세스 전체에 걸리므로 여러 번 호출돼도 한 번만 설정한다.
    if _tracing_configured or config.otel_sdk_disabled:
        return

    # Resource attribute는 Tempo/Grafana에서 서비스를 찾을 때 쓰는 기본 식별자다.
    attributes: dict[str, str] = {SERVICE_NAME: config.service_name}
    if config.service_version:
        attributes[SERVICE_VERSION] = config.service_version
    if config.service_environment:
        attributes[DEPLOYMENT_ENVIRONMENT] = config.service_environment

    provider = TracerProvider(resource=Resource.create(attributes))
    if _otlp_trace_export_enabled(config):
        # exporter가 env를 다시 해석하지 않도록, 앞에서 확정한 endpoint만 넘긴다.
        provider.add_span_processor(BatchSpanProcessor(_otlp_span_exporter(config.otlp_trace_exporter_endpoint)))
    trace.set_tracer_provider(provider)
    _tracing_configured = True


# 아직 process 단위 이름으로 옮기지 못한 호출부를 위한 호환 이름이다.
configure_tracing = configure_process_tracing


def current_trace_context() -> tuple[str, str]:
    span_context = trace.get_current_span().get_span_context()
    if not span_context.is_valid:
        return "", ""
    return format(span_context.trace_id, "032x"), format(span_context.span_id, "016x")


def _otlp_trace_export_enabled(config: ObservabilityConfig) -> bool:
    # trace 전송은 명시적으로 OTLP를 고르고 endpoint도 있을 때만 허용한다.
    traces_exporter = config.otel_traces_exporter.strip().lower()
    if traces_exporter == "none":
        return False
    if traces_exporter != "otlp":
        return False
    return bool(config.otlp_trace_exporter_endpoint)


def _otlp_span_exporter(endpoint: str | None) -> object:
    from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter

    return OTLPSpanExporter(endpoint=endpoint)
