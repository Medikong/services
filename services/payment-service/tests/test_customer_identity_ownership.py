from datetime import UTC, datetime

import anyio
from fastapi.testclient import TestClient

from app.main import create_app
from app.store import PaymentStore
from contracts import OrderCreatedEvent

OWNER_USER_ID = "00000000-0000-4000-8000-000000000001"
OTHER_USER_ID = "00000000-0000-4000-8000-000000000002"
STALE_USER_ID = "00000000-0000-4000-8000-000000000003"


def _payment_store_with_known_order() -> PaymentStore:
    store = PaymentStore()
    anyio.run(
        store.record_order_created,
        OrderCreatedEvent(
            eventId="evt-baseline-payment-owner",
            userId=OWNER_USER_ID,
            sourceId="baseline-order",
            occurredAt=datetime(2026, 7, 17, tzinfo=UTC),
            producer="order-service",
            orderId="baseline-order",
            dropId="drop-001",
            productId="product-001",
            quantity=1,
            amount=50000,
            idempotencyKey="baseline-order-key",
        ),
    )
    return store


def test_payment_amount_mismatch_remains_a_business_conflict() -> None:
    # Given
    store = _payment_store_with_known_order()
    client = TestClient(create_app(store))

    # When
    response = client.post(
        "/payments/mock-approvals",
        headers={
            "X-User-Id": OWNER_USER_ID,
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "baseline-amount-mismatch",
        },
        json={"orderId": "baseline-order", "amount": 60000},
    )

    # Then
    assert response.status_code == 409
    assert response.json()["detail"] == "payment request does not match order"


def test_get_payment_denies_a_different_stored_owner_with_customer_role() -> None:
    # Given
    store = _payment_store_with_known_order()
    client = TestClient(create_app(store))
    created = client.post(
        "/payments/mock-approvals",
        headers={
            "X-User-Id": OWNER_USER_ID,
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "baseline-payment-key",
        },
        json={"orderId": "baseline-order", "amount": 50000},
    )
    assert created.status_code == 201

    # When
    response = client.get(
        f"/payments/{created.json()['data']['id']}",
        headers={
            "X-User-Id": OTHER_USER_ID,
            "X-User-Role": "CUSTOMER",
        },
    )

    # Then
    assert response.status_code == 403
    assert response.json()["detail"] == "payment owner mismatch"


def test_approve_payment_accepts_trusted_user_identity_without_role_header() -> None:
    # Given
    client = TestClient(create_app(_payment_store_with_known_order()))

    # When
    response = client.post(
        "/payments/mock-approvals",
        headers={
            "X-User-Id": OWNER_USER_ID,
            "Idempotency-Key": "role-free-payment-approval",
        },
        json={"orderId": "baseline-order", "amount": 50000},
    )

    # Then
    assert response.status_code == 201
    assert response.json()["data"]["userId"] == OWNER_USER_ID


def test_fail_payment_accepts_trusted_user_identity_without_role_header() -> None:
    # Given
    client = TestClient(create_app(_payment_store_with_known_order()))

    # When
    response = client.post(
        "/payments/mock-failures",
        headers={
            "X-User-Id": OWNER_USER_ID,
            "Idempotency-Key": "role-free-payment-failure",
        },
        json={"orderId": "baseline-order", "amount": 50000},
    )

    # Then
    assert response.status_code == 201
    assert response.json()["data"]["userId"] == OWNER_USER_ID


def test_approve_payment_denies_projection_owner_mismatch_without_role_header() -> (
    None
):
    # Given
    client = TestClient(create_app(_payment_store_with_known_order()))

    # When
    response = client.post(
        "/payments/mock-approvals",
        headers={
            "X-User-Id": OTHER_USER_ID,
            "Idempotency-Key": "role-free-payment-other-key",
        },
        json={"orderId": "baseline-order", "amount": 50000},
    )

    # Then
    assert response.status_code == 403
    assert response.json()["detail"] == "order owner mismatch"


def test_fail_payment_denies_projection_owner_mismatch_without_role_header() -> None:
    # Given
    client = TestClient(create_app(_payment_store_with_known_order()))

    # When
    response = client.post(
        "/payments/mock-failures",
        headers={
            "X-User-Id": OTHER_USER_ID,
            "Idempotency-Key": "role-free-payment-failure-other-key",
        },
        json={"orderId": "baseline-order", "amount": 50000},
    )

    # Then
    assert response.status_code == 403
    assert response.json()["detail"] == "order owner mismatch"


def test_approve_payment_ignores_forged_admin_role_for_projection_mismatch() -> None:
    # Given
    client = TestClient(create_app(_payment_store_with_known_order()))

    # When
    response = client.post(
        "/payments/mock-approvals",
        headers={
            "X-User-Id": OTHER_USER_ID,
            "X-User-Role": "ADMIN",
            "Idempotency-Key": "forged-admin-payment-key",
        },
        json={"orderId": "baseline-order", "amount": 50000},
    )

    # Then
    assert response.status_code == 403
    assert response.json()["detail"] == "order owner mismatch"


def test_amount_mismatch_without_role_header_remains_a_business_conflict() -> None:
    # Given
    client = TestClient(create_app(_payment_store_with_known_order()))

    # When
    response = client.post(
        "/payments/mock-approvals",
        headers={
            "X-User-Id": OWNER_USER_ID,
            "Idempotency-Key": "role-free-amount-mismatch",
        },
        json={"orderId": "baseline-order", "amount": 60000},
    )

    # Then
    assert response.status_code == 409
    assert response.json()["detail"] == "payment request does not match order"


def test_stale_projection_without_role_header_remains_a_business_conflict() -> None:
    # Given
    client = TestClient(create_app(PaymentStore()))

    # When
    response = client.post(
        "/payments/mock-approvals",
        headers={
            "X-User-Id": STALE_USER_ID,
            "Idempotency-Key": "stale-projection-key",
        },
        json={"orderId": "stale-order", "amount": 50000},
    )

    # Then
    assert response.status_code == 409
    assert response.json()["detail"] == "order is not ready for payment"


def test_get_payment_denies_a_different_stored_owner_without_role_header() -> None:
    # Given
    store = _payment_store_with_known_order()
    client = TestClient(create_app(store))
    created = client.post(
        "/payments/mock-approvals",
        headers={
            "X-User-Id": OWNER_USER_ID,
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "role-free-payment-read",
        },
        json={"orderId": "baseline-order", "amount": 50000},
    )
    assert created.status_code == 201

    # When
    response = client.get(
        f"/payments/{created.json()['data']['id']}",
        headers={"X-User-Id": OTHER_USER_ID},
    )

    # Then
    assert response.status_code == 403
    assert response.json()["detail"] == "payment owner mismatch"


def test_get_payment_accepts_its_stored_owner_without_role_header() -> None:
    # Given
    store = _payment_store_with_known_order()
    client = TestClient(create_app(store))
    created = client.post(
        "/payments/mock-approvals",
        headers={
            "X-User-Id": OWNER_USER_ID,
            "Idempotency-Key": "role-free-payment-own-read",
        },
        json={"orderId": "baseline-order", "amount": 50000},
    )
    assert created.status_code == 201

    # When
    response = client.get(
        f"/payments/{created.json()['data']['id']}",
        headers={"X-User-Id": OWNER_USER_ID},
    )

    # Then
    assert response.status_code == 200
    assert response.json()["data"]["userId"] == OWNER_USER_ID


def test_approve_payment_rejects_missing_trusted_user_identity() -> None:
    # Given
    client = TestClient(create_app(_payment_store_with_known_order()))

    # When
    response = client.post(
        "/payments/mock-approvals",
        headers={"Idempotency-Key": "missing-payment-user-key"},
        json={"orderId": "baseline-order", "amount": 50000},
    )

    # Then
    assert response.status_code == 422


def test_approve_payment_rejects_overlong_trusted_user_identity() -> None:
    # Given
    client = TestClient(create_app(_payment_store_with_known_order()))

    # When
    response = client.post(
        "/payments/mock-approvals",
        headers={
            "X-User-Id": "u" * 65,
            "Idempotency-Key": "overlong-payment-user-key",
        },
        json={"orderId": "baseline-order", "amount": 50000},
    )

    # Then
    assert response.status_code == 422


def test_approve_payment_rejects_non_uuid_trusted_user_identity() -> None:
    # Given
    client = TestClient(create_app(_payment_store_with_known_order()))

    # When
    response = client.post(
        "/payments/mock-approvals",
        headers={
            "X-User-Id": "verify-owner",
            "Idempotency-Key": "non-uuid-payment-user-key",
        },
        json={"orderId": "baseline-order", "amount": 50000},
    )

    # Then
    assert response.status_code == 422


def test_payment_runtime_openapi_has_no_user_role_parameter() -> None:
    # Given
    client = TestClient(create_app(PaymentStore()))

    # When
    paths = client.get("/openapi.json").json()["paths"]

    # Then
    for path in (
        "/payments/mock-approvals",
        "/payments/mock-failures",
        "/payments/{payment_id}",
    ):
        for operation in paths[path].values():
            parameter_names = {
                parameter["name"] for parameter in operation.get("parameters", [])
            }
            assert "X-User-Role" not in parameter_names


def test_payment_runtime_openapi_requires_uuid_user_identity() -> None:
    # Given
    client = TestClient(create_app(PaymentStore()))

    # When
    paths = client.get("/openapi.json").json()["paths"]

    # Then
    for path in (
        "/payments/mock-approvals",
        "/payments/mock-failures",
        "/payments/{payment_id}",
    ):
        for operation in paths[path].values():
            user_id_parameter = next(
                parameter
                for parameter in operation.get("parameters", [])
                if parameter["name"] == "X-User-Id"
            )
            assert user_id_parameter["schema"]["format"] == "uuid"
