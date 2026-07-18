import anyio
from contracts import PaymentApprovedEvent
from fastapi.testclient import TestClient
from datetime import UTC, datetime

from app.main import create_app
from app.models import DropId, IdempotencyKey, ProductId, UserId
from app.store import CreateOrderCommand, OrderCreated, OrderStore

OWNER_USER_ID = "00000000-0000-4000-8000-000000000001"
OTHER_USER_ID = "00000000-0000-4000-8000-000000000002"


def test_get_order_denies_a_different_stored_owner_with_customer_role() -> None:
    # Given
    store = OrderStore()
    created = anyio.run(
        store.create_order,
        CreateOrderCommand(
            user_id=UserId(OWNER_USER_ID),
            drop_id=DropId("drop-001"),
            product_id=ProductId("product-001"),
            quantity=1,
            idempotency_key=IdempotencyKey("baseline-order-owner-key"),
        ),
    )
    assert isinstance(created, OrderCreated)
    client = TestClient(create_app(store))

    # When
    response = client.get(
        f"/orders/{created.order.id}",
        headers={
            "X-User-Id": OTHER_USER_ID,
            "X-User-Role": "CUSTOMER",
        },
    )

    # Then
    assert response.status_code == 403
    assert response.json()["error"]["message"] == "order owner mismatch"


def test_create_order_accepts_trusted_user_identity_without_role_header() -> None:
    # Given
    client = TestClient(create_app(OrderStore()))

    # When
    response = client.post(
        "/orders",
        headers={
            "X-User-Id": OWNER_USER_ID,
            "Idempotency-Key": "role-free-order-key",
        },
        json={"dropId": "drop-001", "productId": "product-001", "quantity": 1},
    )

    # Then
    assert response.status_code == 201
    assert response.json()["data"]["userId"] == OWNER_USER_ID


def test_get_order_denies_a_different_stored_owner_without_role_header() -> None:
    # Given
    store = OrderStore()
    created = _create_order(store, OWNER_USER_ID, "role-free-read")
    client = TestClient(create_app(store))

    # When
    response = client.get(
        f"/orders/{created.order.id}",
        headers={"X-User-Id": OTHER_USER_ID},
    )

    # Then
    assert response.status_code == 403
    assert response.json()["error"]["message"] == "order owner mismatch"


def test_get_order_accepts_its_stored_owner_without_role_header() -> None:
    # Given
    store = OrderStore()
    created = _create_order(store, OWNER_USER_ID, "role-free-own-read")
    client = TestClient(create_app(store))

    # When
    response = client.get(
        f"/orders/{created.order.id}",
        headers={"X-User-Id": OWNER_USER_ID},
    )

    # Then
    assert response.status_code == 200
    assert response.json()["data"]["userId"] == OWNER_USER_ID


def test_get_order_ignores_forged_admin_role_for_a_different_owner() -> None:
    # Given
    store = OrderStore()
    created = _create_order(store, OWNER_USER_ID, "forged-admin-read")
    client = TestClient(create_app(store))

    # When
    response = client.get(
        f"/orders/{created.order.id}",
        headers={
            "X-User-Id": OTHER_USER_ID,
            "X-User-Role": "ADMIN",
        },
    )

    # Then
    assert response.status_code == 403
    assert response.json()["error"]["message"] == "order owner mismatch"


def test_create_order_rejects_empty_trusted_user_identity() -> None:
    # Given
    client = TestClient(create_app(OrderStore()))

    # When
    response = client.post(
        "/orders",
        headers={
            "X-User-Id": "",
            "Idempotency-Key": "empty-order-user-key",
        },
        json={"dropId": "drop-001", "productId": "product-001", "quantity": 1},
    )

    # Then
    assert response.status_code == 422


def test_create_order_rejects_missing_trusted_user_identity() -> None:
    # Given
    client = TestClient(create_app(OrderStore()))

    # When
    response = client.post(
        "/orders",
        headers={"Idempotency-Key": "missing-order-user-key"},
        json={"dropId": "drop-001", "productId": "product-001", "quantity": 1},
    )

    # Then
    assert response.status_code == 422


def test_create_order_rejects_overlong_trusted_user_identity() -> None:
    # Given
    client = TestClient(create_app(OrderStore()))

    # When
    response = client.post(
        "/orders",
        headers={
            "X-User-Id": "u" * 65,
            "Idempotency-Key": "overlong-order-user-key",
        },
        json={"dropId": "drop-001", "productId": "product-001", "quantity": 1},
    )

    # Then
    assert response.status_code == 422


def test_create_order_rejects_non_uuid_trusted_user_identity() -> None:
    # Given
    client = TestClient(create_app(OrderStore()))

    # When
    response = client.post(
        "/orders",
        headers={
            "X-User-Id": "verify-owner",
            "Idempotency-Key": "non-uuid-order-user-key",
        },
        json={"dropId": "drop-001", "productId": "product-001", "quantity": 1},
    )

    # Then
    assert response.status_code == 422


def test_cancellation_denies_a_different_owner_without_role_header() -> None:
    # Given
    store = OrderStore()
    created = _confirmed_order(store, "role-free-cancel")
    client = TestClient(create_app(store))

    # When
    response = client.post(
        f"/orders/{created.order.id}/cancellations",
        headers={
            "X-User-Id": OTHER_USER_ID,
            "Idempotency-Key": "role-free-cancel-key",
        },
        json={"reason": "customer request"},
    )

    # Then
    assert response.status_code == 403
    assert response.json()["error"]["message"] == "order owner mismatch"


def test_cancellation_accepts_its_owner_without_role_header() -> None:
    # Given
    store = OrderStore()
    created = _confirmed_order(store, "role-free-own-cancel")
    client = TestClient(create_app(store))

    # When
    response = client.post(
        f"/orders/{created.order.id}/cancellations",
        headers={
            "X-User-Id": created.order.userId,
            "Idempotency-Key": "role-free-own-cancel-key",
        },
        json={"reason": "customer request"},
    )

    # Then
    assert response.status_code == 202
    assert response.json()["data"]["orderId"] == created.order.id


def test_cancellation_status_accepts_its_owner_without_role_header() -> None:
    # Given
    store = OrderStore()
    created = _confirmed_order(store, "role-free-own-cancel-status")
    client = TestClient(create_app(store))
    accepted = client.post(
        f"/orders/{created.order.id}/cancellations",
        headers={
            "X-User-Id": created.order.userId,
            "Idempotency-Key": "role-free-own-cancel-status-key",
        },
        json={"reason": "customer request"},
    )
    assert accepted.status_code == 202

    # When
    response = client.get(
        f"/orders/{created.order.id}/cancellations",
        headers={"X-User-Id": created.order.userId},
    )

    # Then
    assert response.status_code == 200
    assert response.json()["data"]["orderId"] == created.order.id


def test_cancellation_status_denies_a_different_owner_without_role_header() -> None:
    # Given
    store = OrderStore()
    created = _confirmed_order(store, "role-free-cancel-status-other")
    client = TestClient(create_app(store))
    accepted = client.post(
        f"/orders/{created.order.id}/cancellations",
        headers={
            "X-User-Id": created.order.userId,
            "Idempotency-Key": "role-free-cancel-status-other-key",
        },
        json={"reason": "customer request"},
    )
    assert accepted.status_code == 202

    # When
    response = client.get(
        f"/orders/{created.order.id}/cancellations",
        headers={"X-User-Id": OTHER_USER_ID},
    )

    # Then
    assert response.status_code == 403
    assert response.json()["error"]["message"] == "order owner mismatch"


def test_order_runtime_openapi_has_no_user_role_parameter() -> None:
    # Given
    client = TestClient(create_app(OrderStore()))

    # When
    paths = client.get("/openapi.json").json()["paths"]

    # Then
    for path in (
        "/orders",
        "/orders/{order_id}",
        "/orders/{order_id}/cancellations",
    ):
        for operation in paths[path].values():
            parameter_names = {
                parameter["name"] for parameter in operation.get("parameters", [])
            }
            assert "X-User-Role" not in parameter_names


def test_order_runtime_openapi_requires_uuid_user_identity() -> None:
    # Given
    client = TestClient(create_app(OrderStore()))

    # When
    paths = client.get("/openapi.json").json()["paths"]

    # Then
    for path in (
        "/orders",
        "/orders/{order_id}",
        "/orders/{order_id}/cancellations",
    ):
        for operation in paths[path].values():
            user_id_parameter = next(
                parameter
                for parameter in operation.get("parameters", [])
                if parameter["name"] == "X-User-Id"
            )
            assert user_id_parameter["schema"]["format"] == "uuid"


def _create_order(store: OrderStore, user_id: str, suffix: str) -> OrderCreated:
    created = anyio.run(
        store.create_order,
        CreateOrderCommand(
            user_id=UserId(user_id),
            drop_id=DropId("drop-001"),
            product_id=ProductId("product-001"),
            quantity=1,
            idempotency_key=IdempotencyKey(f"order-{suffix}"),
        ),
    )
    assert isinstance(created, OrderCreated)
    return created


def _confirmed_order(store: OrderStore, suffix: str) -> OrderCreated:
    created = _create_order(store, OWNER_USER_ID, suffix)
    anyio.run(
        store.apply_payment_approved,
        PaymentApprovedEvent(
            eventId=f"evt-approved-{suffix}",
            userId=created.order.userId,
            sourceId=f"payment-{suffix}",
            occurredAt=datetime(2026, 7, 17, tzinfo=UTC),
            producer="payment-service",
            orderId=created.order.id,
            paymentId=f"payment-{suffix}",
            amount=created.order.amount,
        ),
    )
    return created
