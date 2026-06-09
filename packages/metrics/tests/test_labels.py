import pytest
from prometheus_client import CollectorRegistry, generate_latest

from metrics import (
    HTTP_ACTIVE_REQUEST_LABELS,
    HTTP_REQUEST_DURATION_LABELS,
    SERVICE_READY_LABELS,
    ServiceIdentity,
    assert_safe_metric_label_names,
    http_server_active_requests,
    http_server_request_duration_seconds,
    service_ready,
)


def test_common_http_label_names_match_prometheus_policy() -> None:
    assert HTTP_REQUEST_DURATION_LABELS == (
        "service_name",
        "service_version",
        "service_environment",
        "http_route",
        "http_request_method",
        "http_response_status_code",
    )
    assert HTTP_ACTIVE_REQUEST_LABELS == (
        "service_name",
        "service_version",
        "service_environment",
        "http_route",
        "http_request_method",
    )
    assert SERVICE_READY_LABELS == (
        "service_name",
        "service_version",
        "service_environment",
    )


def test_common_operational_metric_factories_create_prometheus_handles() -> None:
    registry = CollectorRegistry(auto_describe=True)

    http_server_request_duration_seconds(registry)
    http_server_active_requests(registry)
    ready_metric = service_ready(registry)
    ready_metric.labels(
        service_name="payment-service",
        service_version="test",
        service_environment="local",
    ).set(1)

    metrics_text = generate_latest(registry).decode("utf-8")

    assert "# TYPE http_server_request_duration_seconds histogram" in metrics_text
    assert "# TYPE http_server_active_requests gauge" in metrics_text
    assert "# TYPE service_ready gauge" in metrics_text
    assert 'service_name="payment-service"' in metrics_text


def test_service_identity_requires_service_version_and_environment() -> None:
    identity = ServiceIdentity(
        service_name="payment-service",
        service_version="test-version",
        service_environment="test",
    )

    assert identity.service_labels() == {
        "service_name": "payment-service",
        "service_version": "test-version",
        "service_environment": "test",
    }

    with pytest.raises(ValueError, match="service_version"):
        ServiceIdentity(service_name="payment-service", service_version="", service_environment="test")

    with pytest.raises(ValueError, match="service_environment"):
        ServiceIdentity(service_name="payment-service", service_version="test-version", service_environment="")


def test_high_cardinality_labels_are_rejected() -> None:
    with pytest.raises(ValueError, match="request_id"):
        assert_safe_metric_label_names(["service_name", "request_id"])
