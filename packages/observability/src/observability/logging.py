import logging

import structlog


_logging_configured = False
_logging_instrumented = False


def configure_process_logging() -> None:
    """서비스 시작 시 프로세스 전체 로깅을 설정한다.

    이 함수는 순수 함수가 아니다. 현재 프로세스의 Python logging,
    structlog, OpenTelemetry logging instrumentation 상태를 바꾼다.
    서비스 앱 부트스트랩에서만 호출해서, 비즈니스 코드는 전역 로깅 설정을 몰라도 되게 한다.
    """
    global _logging_configured, _logging_instrumented

    if not _logging_instrumented:
        _instrument_logging()
        _logging_instrumented = True

    if _logging_configured:
        return

    logging.basicConfig(level=logging.INFO, format="%(message)s")
    structlog.configure(
        processors=[
            structlog.contextvars.merge_contextvars,
            structlog.processors.TimeStamper(key="timestamp", fmt="iso", utc=True),
            structlog.processors.JSONRenderer(separators=(",", ":")),
        ],
        logger_factory=structlog.stdlib.LoggerFactory(),
        wrapper_class=structlog.stdlib.BoundLogger,
        cache_logger_on_first_use=True,
    )
    _logging_configured = True


# 아직 process 단위 이름으로 옮기지 못한 호출부를 위한 호환 이름이다.
configure_structured_logging = configure_process_logging


def _instrument_logging() -> None:
    from opentelemetry.instrumentation.logging import LoggingInstrumentor

    LoggingInstrumentor().instrument(set_logging_format=False)
