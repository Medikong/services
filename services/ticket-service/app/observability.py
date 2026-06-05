from fastapi import FastAPI
from observability import (
    ErrorRecordingMiddleware,
    ObservabilityConfig,
    configure_process_logging,
    configure_process_tracing,
    create_request_log_middleware,
    instrument_fastapi_app,
)
from server import (
    RequestContextMiddleware,
    ResponseHeadersMiddleware,
    RuntimeRecoveryMiddleware,
    request_context_middleware_options,
)


def configure_app_observability(app: FastAPI, config: ObservabilityConfig) -> None:
    # 프로세스 설정에서 tracer와 전송 exporter를 붙인다. 요청 처리 코드가 span을 직접 보내지 않는다.
    configure_process_logging()
    configure_process_tracing(config)
    # FastAPI 계측이 HTTP 요청 span을 자동으로 만들고, 요청이 끝나면 전송 흐름으로 넘긴다.
    instrument_fastapi_app(app, config)
    # 요청 로그에는 현재 span ID를 함께 남겨 stdout 로그와 trace를 나중에 이어 볼 수 있게 한다.
    app.middleware("http")(create_request_log_middleware(config))
    # 예외 기록은 복구 응답보다 안쪽에서 수행하고, 응답 생성은 RuntimeRecoveryMiddleware에 맡긴다.
    app.add_middleware(ErrorRecordingMiddleware, service_name=config.service_name)
    # FastAPI는 마지막에 등록한 미들웨어를 요청 처리 때 먼저 실행한다.
    app.add_middleware(RuntimeRecoveryMiddleware)
    app.add_middleware(ResponseHeadersMiddleware)
    app.add_middleware(RequestContextMiddleware, **request_context_middleware_options())
