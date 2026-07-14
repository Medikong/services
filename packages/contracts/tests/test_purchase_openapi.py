from pathlib import Path
from typing import TypeAlias

import yaml


REPOSITORY_ROOT = Path(__file__).resolve().parents[3]
YamlValue: TypeAlias = (
    str | int | bool | None | list["YamlValue"] | dict[str, "YamlValue"]
)


def load_openapi(service: str) -> dict[str, YamlValue]:
    contract_path = REPOSITORY_ROOT / "contracts" / "services" / service / "openapi.yaml"
    with contract_path.open(encoding="utf-8") as contract_file:
        return yaml.safe_load(contract_file)


def test_order_contract_defines_idempotent_customer_cancellation() -> None:
    contract = load_openapi("order-service")
    operation = contract["paths"]["/orders/{order_id}/cancellations"]["post"]
    parameters = operation["parameters"]

    assert any(
        parameter.get("$ref", "").endswith("/Idempotency-Key")
        for parameter in parameters
    )
    assert any(
        parameter.get("name") == "X-User-Role"
        and parameter["schema"]["enum"] == ["CUSTOMER"]
        for parameter in parameters
    )
    assert operation["requestBody"]["content"]["application/json"]["schema"] == {
        "$ref": "#/components/schemas/CancelOrderRequest"
    }
    assert operation["responses"]["202"]["x-idempotent-replay"] is True
    assert "SHIPPED" in operation["responses"]["409"]["description"]
    request_schema = contract["components"]["schemas"]["CancelOrderRequest"]
    assert request_schema["required"] == ["reason"]


def test_order_contract_defines_lifecycle_and_fulfillment_states() -> None:
    schemas = load_openapi("order-service")["components"]["schemas"]

    assert schemas["OrderStatus"]["enum"] == [
        "PENDING_PAYMENT",
        "CONFIRMED",
        "PAYMENT_FAILED",
        "CANCEL_PENDING",
        "CANCELED",
        "EXPIRED",
    ]
    assert schemas["FulfillmentStatus"]["enum"] == [
        "NOT_STARTED",
        "PREPARING",
        "SHIPPED",
    ]
    assert schemas["OrderStatus"]["x-allowed-transitions"] == {
        "PENDING_PAYMENT": ["CONFIRMED", "PAYMENT_FAILED", "EXPIRED"],
        "CONFIRMED": ["CANCEL_PENDING"],
        "CANCEL_PENDING": ["CANCELED"],
    }


def test_payment_contract_defines_refund_ledger_states() -> None:
    schemas = load_openapi("payment-service")["components"]["schemas"]

    assert schemas["RefundStatus"]["enum"] == [
        "REQUESTED",
        "PROCESSING",
        "COMPLETED",
        "FAILED",
    ]
    assert schemas["Refund"]["properties"]["status"] == {
        "$ref": "#/components/schemas/RefundStatus"
    }


def test_notification_contract_defines_all_purchase_outcomes() -> None:
    schemas = load_openapi("notification-service")["components"]["schemas"]

    assert schemas["NotificationType"]["enum"] == [
        "ORDER_CONFIRMED",
        "PAYMENT_FAILED",
        "ORDER_EXPIRED",
        "ORDER_CANCELED",
        "PAYMENT_REFUNDED",
        "REFUND_FAILED",
    ]
