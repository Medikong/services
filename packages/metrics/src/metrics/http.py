from enum import StrEnum

from prometheus_client import CollectorRegistry, Gauge, Histogram

from metrics.labels import CommonServiceLabel, assert_safe_metric_label_names


class HttpRequestDurationLabel(StrEnum):
    # HTTP duration label 정책
    # - 의미: OTel semantic convention과 정렬
    # - 표기: Prometheus label 관례에 맞춰 snake_case 사용
    # - 금지: raw path, request ID, user/domain object ID
    SERVICE_NAME = CommonServiceLabel.SERVICE_NAME.value
    SERVICE_VERSION = CommonServiceLabel.SERVICE_VERSION.value
    SERVICE_ENVIRONMENT = CommonServiceLabel.SERVICE_ENVIRONMENT.value
    HTTP_ROUTE = "http_route"
    HTTP_ROUTE_KIND = "http_route_kind"
    HTTP_REQUEST_METHOD = "http_request_method"
    HTTP_RESPONSE_STATUS_CODE = "http_response_status_code"


class HttpActiveRequestLabel(StrEnum):
    # HTTP active request label 정책
    # - 기준: 현재 처리 중인 요청 수를 route/method 단위로 집계
    # - 제외: status code, raw path, request ID
    SERVICE_NAME = CommonServiceLabel.SERVICE_NAME.value
    SERVICE_VERSION = CommonServiceLabel.SERVICE_VERSION.value
    SERVICE_ENVIRONMENT = CommonServiceLabel.SERVICE_ENVIRONMENT.value
    HTTP_ROUTE = "http_route"
    HTTP_ROUTE_KIND = "http_route_kind"
    HTTP_REQUEST_METHOD = "http_request_method"


HTTP_REQUEST_DURATION_LABELS = tuple(label.value for label in HttpRequestDurationLabel)
HTTP_ACTIVE_REQUEST_LABELS = tuple(label.value for label in HttpActiveRequestLabel)
HTTP_METHODS = frozenset(
    {"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "CONNECT", "OPTIONS", "TRACE"}
)
assert_safe_metric_label_names(HTTP_REQUEST_DURATION_LABELS)
assert_safe_metric_label_names(HTTP_ACTIVE_REQUEST_LABELS)


def bounded_http_method(method: str) -> str:
    normalized = method.strip().upper()
    return normalized if normalized in HTTP_METHODS else "OTHER"


def http_server_request_duration_seconds(registry: CollectorRegistry) -> Histogram:
    return Histogram(
        "http_server_request_duration_seconds",
        "HTTP server request duration in seconds.",
        HTTP_REQUEST_DURATION_LABELS,
        registry=registry,
    )


def http_server_active_requests(registry: CollectorRegistry) -> Gauge:
    return Gauge(
        "http_server_active_requests",
        "Currently active HTTP server requests.",
        HTTP_ACTIVE_REQUEST_LABELS,
        registry=registry,
    )
