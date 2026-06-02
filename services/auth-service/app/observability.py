from server.observability import get_current_request_id, setup_request_observability


def setup_request_logging(app, service_name: str) -> None:
    setup_request_observability(app, service_name)
