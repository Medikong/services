from observability.config import ObservabilityConfig
from observability.logging import configure_process_logging
from observability.profiling import configure_process_profiling
from observability.tracing import configure_process_tracing


def configure_process_observability(config: ObservabilityConfig) -> None:
    """프로세스 단위 관측성 설정을 HTTP/worker 진입점에서 공유한다."""
    configure_process_logging()
    configure_process_profiling(config)
    configure_process_tracing(config)
