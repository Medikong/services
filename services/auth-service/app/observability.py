from fastapi import FastAPI
from observability import (
    ObservabilityConfig,
    configure_process_logging,
    configure_process_tracing,
    create_request_log_middleware,
    instrument_fastapi_app,
)
from server import install_runtime_middleware


def configure_app_observability(app: FastAPI, config: ObservabilityConfig) -> None:
    # 프로세스 설정에서 tracer와 전송 exporter를 붙인다. 요청 처리 코드가 span을 직접 보내지 않는다.
    configure_process_logging()
    configure_process_tracing(config)
    # FastAPI 계측이 HTTP 요청 span을 자동으로 만들고, 요청이 끝나면 전송 흐름으로 넘긴다.
    instrument_fastapi_app(app)
    # 요청 로그에는 현재 span ID를 함께 남겨 stdout 로그와 trace를 나중에 이어 볼 수 있게 한다.
    app.middleware("http")(create_request_log_middleware(config))
    # 공통 런타임 미들웨어도 서비스 부트스트랩에서 붙인다. 관측성 패키지가 app에 직접 주입하지 않는다.
    install_runtime_middleware(app)
