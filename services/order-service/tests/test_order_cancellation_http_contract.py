from datetime import datetime

from fastapi.testclient import TestClient

from app.main import create_app
from app.cancellations import RequestCancellationCommand, RequestCancellationResult
from app.store import OrderStore


class PrivateDatabaseFailure(RuntimeError):
    pass


class FailingCancellationStore(OrderStore):
    async def request_cancellation(
        self,
        command: RequestCancellationCommand,
    ) -> RequestCancellationResult:
        del command
        raise PrivateDatabaseFailure("private database failure")


def test_cancellation_openapi_documents_static_operation_metadata() -> None:
    # Given
    operation = (
        TestClient(create_app(OrderStore()))
        .get("/openapi.json")
        .json()["paths"]["/orders/{order_id}/cancellations"]["post"]
    )

    # When
    parameters = {parameter["name"]: parameter for parameter in operation["parameters"]}

    # Then
    assert operation["summary"] == "배송 전 주문 취소 및 전액 환불 요청"
    assert operation["description"] == (
        "CONFIRMED 주문의 배송이 시작되기 전에 취소를 접수한다. 같은 "
        "Idempotency-Key와 같은 reason의 재실행은 최초 접수 결과를 그대로 반환한다."
    )
    assert operation["responses"]["202"]["x-idempotent-replay"] is True
    assert operation["responses"]["202"]["description"] == (
        "취소와 전액 환불이 접수되었다. 멱등 재실행은 같은 취소 접수 결과를 반환한다."
    )
    assert parameters["X-Request-Id"]["required"] is False
    assert parameters["X-Request-Id"]["description"] == (
        "Request correlation id. If absent, the server may generate one."
    )
    assert parameters["traceparent"]["required"] is False
    assert parameters["traceparent"]["description"] == ("W3C trace context header.")

    status_operation = (
        TestClient(create_app(OrderStore()))
        .get("/openapi.json")
        .json()["paths"]["/orders/{order_id}/cancellations"]["get"]
    )
    status_parameters = {
        parameter["name"]: parameter for parameter in status_operation["parameters"]
    }
    assert set(status_operation["responses"]) == {
        "200",
        "401",
        "403",
        "404",
        "422",
        "500",
    }
    assert status_operation["responses"]["200"]["description"] == (
        "현재 취소 및 환불 처리 상태."
    )
    assert status_operation["responses"]["403"]["description"] == (
        "Authenticated user is not allowed to access the resource."
    )
    assert status_operation["responses"]["404"]["description"] == (
        "Resource not found."
    )
    assert status_operation["responses"]["422"]["description"] == (
        "Request is syntactically valid but violates domain rules."
    )
    assert status_operation["responses"]["500"]["description"] == (
        "Unexpected server error."
    )
    assert status_parameters["X-User-Id"]["description"] == (
        "Authenticated user id forwarded by the trusted gateway in local E2E."
    )
    assert status_parameters["X-Request-Id"]["description"] == (
        "Request correlation id. If absent, the server may generate one."
    )
    assert status_parameters["traceparent"]["description"] == (
        "W3C trace context header."
    )


def test_forged_admin_header_does_not_change_missing_order_envelope() -> None:
    # Given
    client = TestClient(create_app(OrderStore()))

    # When
    response = client.post(
        "/orders/order-role-forbidden/cancellations",
        headers={
            "X-User-Id": "00000000-0000-4000-8000-000000000003",
            "X-User-Role": "ADMIN",
            "Idempotency-Key": "cancel-role-forbidden",
            "X-Request-Id": "request-role-forbidden",
        },
        json={"reason": "customer request"},
    )

    # Then
    assert response.status_code == 404
    assert response.json()["error"] == {
        "code": "order.not_found",
        "message": "order not found",
        "details": {"orderId": "order-role-forbidden"},
    }
    assert response.json()["requestId"] == "request-role-forbidden"
    assert datetime.fromisoformat(
        response.json()["occurredAt"].replace("Z", "+00:00")
    ).tzinfo


def test_invalid_cancellation_error_uses_common_envelope() -> None:
    # Given
    client = TestClient(create_app(OrderStore()))

    # When
    response = client.post(
        "/orders/order-validation/cancellations",
        headers={
            "X-User-Id": "00000000-0000-4000-8000-000000000004",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "",
        },
        json={"reason": "customer request"},
    )

    # Then
    assert response.status_code == 422
    assert response.json()["error"]["code"] == "request.validation_failed"
    assert response.json()["error"]["message"] == "Request validation failed."
    assert "errors" in response.json()["error"]["details"]
    assert response.json()["requestId"] == response.headers["X-Request-Id"]


def test_unexpected_cancellation_error_uses_safe_common_envelope() -> None:
    # Given
    client = TestClient(
        create_app(FailingCancellationStore()),
        raise_server_exceptions=False,
    )

    # When
    response = client.post(
        "/orders/order-unexpected/cancellations",
        headers={
            "X-User-Id": "00000000-0000-4000-8000-000000000005",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "cancel-unexpected",
            "X-Request-Id": "request-unexpected",
        },
        json={"reason": "customer request"},
    )

    # Then
    assert response.status_code == 500
    assert response.json()["error"] == {
        "code": "order.internal_error",
        "message": "Unexpected server error.",
    }
    assert "private database failure" not in response.text
    assert response.json()["requestId"] == "request-unexpected"
