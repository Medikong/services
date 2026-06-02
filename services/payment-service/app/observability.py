from server.observability import ObservabilityConfig, get_current_request_id, setup_request_observability


def setup_request_logging(app, config: ObservabilityConfig) -> None:
    setup_request_observability(app, config)
