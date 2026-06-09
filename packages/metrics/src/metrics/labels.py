from collections.abc import Iterable
from dataclasses import dataclass
from enum import StrEnum


# 고카디널리티 label 금지 목록
# - 목적: Prometheus 시계열 폭증 방지
# - metric: 서비스/route/status 같은 낮은 cardinality 차원만 기록
# - log/trace: request_id, trace_id, domain object ID 같은 요청별 값 기록
FORBIDDEN_HIGH_CARDINALITY_LABELS = frozenset(
    {
        "request_id",
        "trace_id",
        "span_id",
        "correlation_id",
        "user_id",
        "order_id",
        "payment_id",
        "reservation_id",
        "ticket_id",
        "path",
        "raw_path",
    }
)


class CommonServiceLabel(StrEnum):
    SERVICE_NAME = "service_name"
    SERVICE_VERSION = "service_version"
    SERVICE_ENVIRONMENT = "service_environment"


class MetricResult(StrEnum):
    SUCCESS = "success"
    FAILURE = "failure"
    REJECTION = "rejection"
    DUPLICATE = "duplicate"
    SKIPPED = "skipped"


class FailureKind(StrEnum):
    NONE = "none"
    BUSINESS_REJECTION = "business_rejection"
    INTERNAL_ERROR = "internal_error"
    DEPENDENCY_ERROR = "dependency_error"


class Expected(StrEnum):
    TRUE = "true"
    FALSE = "false"


COMMON_SERVICE_LABELS = tuple(label.value for label in CommonServiceLabel)


@dataclass(frozen=True)
class ServiceIdentity:
    # 공통 서비스 식별 label
    # - 필수: service_name
    # - 필수: service_version
    # - 필수: service_environment
    # - 실패: 호출자가 외부 설정/env를 해석하지 못한 경우 즉시 예외
    service_name: str
    service_version: str
    service_environment: str

    def __post_init__(self) -> None:
        _require_label_value("service_name", self.service_name)
        _require_label_value("service_version", self.service_version)
        _require_label_value("service_environment", self.service_environment)

    def service_labels(self) -> dict[str, str]:
        return {
            CommonServiceLabel.SERVICE_NAME.value: self.service_name,
            CommonServiceLabel.SERVICE_VERSION.value: self.service_version,
            CommonServiceLabel.SERVICE_ENVIRONMENT.value: self.service_environment,
        }


def assert_safe_metric_label_names(label_names: Iterable[str]) -> None:
    forbidden = sorted(FORBIDDEN_HIGH_CARDINALITY_LABELS.intersection(label_names))
    if forbidden:
        raise ValueError(f"high-cardinality metric labels are not allowed: {', '.join(forbidden)}")


def _require_label_value(name: str, value: str | None) -> None:
    if value is None or value == "":
        raise ValueError(f"{name} is required")
