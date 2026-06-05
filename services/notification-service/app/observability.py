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
    # Process setup creates the tracer provider/exporter; request handlers do not send spans manually.
    configure_process_logging()
    configure_process_tracing(config)
    # FastAPI instrumentation creates inbound request spans automatically and exports them when spans end.
    instrument_fastapi_app(app)
    # Request logs read the current span IDs so stdout logs can be joined with exported traces.
    app.middleware("http")(create_request_log_middleware(config))
    # Runtime middleware is still installed by the service bootstrap, not by the observability package.
    install_runtime_middleware(app)
