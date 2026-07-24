from __future__ import annotations

import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import aws_purchase_scenario_contracts as contracts
from aws_purchase_scenario_models import JsonObject, Scenario


def _response() -> JsonObject:
    return {
        "content": {
            "application/json": {
                "schema": {"type": "object"},
            },
        },
    }


def _failure_operation() -> JsonObject:
    return {
        "requestBody": {
            "required": True,
            "content": {
                "application/json": {
                    "schema": {"type": "object"},
                },
            },
        },
        "responses": {
            "201": _response(),
            "409": _response(),
        },
    }


def _documents(failure_operation: JsonObject) -> dict[str, JsonObject]:
    response = _response()
    return {
        "catalog-service": {
            "paths": {
                "/drops/{dropId}": {
                    "get": {"responses": {"200": response}},
                },
            },
        },
        "order-service": {
            "paths": {
                "/orders": {
                    "post": {
                        "responses": {
                            "201": response,
                            "409": response,
                        },
                    },
                },
                "/orders/{orderId}": {
                    "get": {
                        "responses": {
                            "200": response,
                            "404": response,
                        },
                    },
                },
            },
        },
        "payment-service": {
            "paths": {
                "/payments/mock-approvals": {
                    "post": {
                        "responses": {
                            "201": response,
                            "409": response,
                        },
                    },
                },
                "/payments/{paymentId}": {
                    "get": {
                        "responses": {
                            "200": response,
                            "404": response,
                        },
                    },
                },
                "/payments/mock-failures": {"post": failure_operation},
            },
        },
        "notification-service": {
            "paths": {
                "/notifications": {
                    "get": {"responses": {"200": response}},
                },
            },
        },
    }


def _prerequisites(
    monkeypatch: pytest.MonkeyPatch,
    scenario: Scenario,
    failure_operation: JsonObject,
) -> tuple[str, ...]:
    documents = _documents(failure_operation)

    def load_contract(_root: Path, service: str) -> JsonObject:
        return documents[service]

    monkeypatch.setattr(contracts, "_load_contract", load_contract)
    return contracts.contract_prerequisites(Path("."), scenario)


@pytest.mark.parametrize(
    "scenario",
    [Scenario.PAYMENT_FAILURE, Scenario.LOW_STOCK_CONCURRENCY],
)
def test_failure_route_without_request_schema_blocks(
    monkeypatch: pytest.MonkeyPatch,
    scenario: Scenario,
) -> None:
    operation = _failure_operation()
    del operation["requestBody"]

    missing = _prerequisites(monkeypatch, scenario, operation)

    assert "POST /payments/mock-failures" in missing
    assert "OpenAPI payment failure request/response schema" in missing


@pytest.mark.parametrize(
    "scenario",
    [Scenario.PAYMENT_FAILURE, Scenario.LOW_STOCK_CONCURRENCY],
)
def test_failure_route_with_empty_request_schema_blocks(
    monkeypatch: pytest.MonkeyPatch,
    scenario: Scenario,
) -> None:
    operation = _failure_operation()
    request_body = operation["requestBody"]
    assert isinstance(request_body, dict)
    content = request_body["content"]
    assert isinstance(content, dict)
    media_type = content["application/json"]
    assert isinstance(media_type, dict)
    media_type["schema"] = {}

    missing = _prerequisites(monkeypatch, scenario, operation)

    assert "POST /payments/mock-failures" in missing
    assert "OpenAPI payment failure request/response schema" in missing


@pytest.mark.parametrize(
    ("scenario", "status"),
    [
        (Scenario.PAYMENT_FAILURE, "201"),
        (Scenario.LOW_STOCK_CONCURRENCY, "409"),
    ],
)
def test_failure_route_without_response_schema_blocks(
    monkeypatch: pytest.MonkeyPatch,
    scenario: Scenario,
    status: str,
) -> None:
    operation = _failure_operation()
    responses = operation["responses"]
    assert isinstance(responses, dict)
    del responses[status]
    responses[status] = {"description": "schema missing"}

    missing = _prerequisites(monkeypatch, scenario, operation)

    assert "POST /payments/mock-failures" in missing
    assert "OpenAPI payment failure request/response schema" in missing


@pytest.mark.parametrize(
    ("scenario", "status"),
    [
        (Scenario.PAYMENT_FAILURE, "201"),
        (Scenario.PAYMENT_FAILURE, "409"),
        (Scenario.LOW_STOCK_CONCURRENCY, "201"),
        (Scenario.LOW_STOCK_CONCURRENCY, "409"),
    ],
)
def test_failure_route_with_empty_response_schema_blocks(
    monkeypatch: pytest.MonkeyPatch,
    scenario: Scenario,
    status: str,
) -> None:
    operation = _failure_operation()
    responses = operation["responses"]
    assert isinstance(responses, dict)
    response = responses[status]
    assert isinstance(response, dict)
    content = response["content"]
    assert isinstance(content, dict)
    media_type = content["application/json"]
    assert isinstance(media_type, dict)
    media_type["schema"] = {}

    missing = _prerequisites(monkeypatch, scenario, operation)

    assert "POST /payments/mock-failures" in missing
    assert "OpenAPI payment failure request/response schema" in missing


def test_failure_route_with_request_and_response_schemas_is_supported(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    missing = _prerequisites(
        monkeypatch,
        Scenario.PAYMENT_FAILURE,
        _failure_operation(),
    )

    assert missing == ()
