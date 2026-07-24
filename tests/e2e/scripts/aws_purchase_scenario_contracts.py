from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
from typing import Final, assert_never

import yaml
from pydantic import TypeAdapter, ValidationError

from aws_purchase_scenario_models import (
    JsonObject,
    JsonValue,
    RunnerStop,
    Scenario,
    Verdict,
)

_JSON_ADAPTER: Final = TypeAdapter(JsonValue)
_SCHEMA_KEYS: Final = frozenset(
    {
        "type",
        "properties",
        "required",
        "items",
        "prefixItems",
        "allOf",
        "anyOf",
        "oneOf",
        "not",
        "enum",
        "const",
        "additionalProperties",
    }
)


@dataclass(frozen=True, slots=True)
class OperationRequirement:
    service: str
    method: str
    path: str
    statuses: tuple[str, ...]
    require_schemas: bool = False

    @property
    def label(self) -> str:
        return f"{self.method.upper()} {self.path}"


_HAPPY_PATH_REQUIREMENTS: Final = (
    OperationRequirement("catalog-service", "get", "/drops/{dropId}", ("200",)),
    OperationRequirement("order-service", "post", "/orders", ("201", "409")),
    OperationRequirement(
        "order-service",
        "get",
        "/orders/{orderId}",
        ("200", "404"),
    ),
    OperationRequirement(
        "payment-service",
        "post",
        "/payments/mock-approvals",
        ("201", "409"),
    ),
    OperationRequirement(
        "payment-service",
        "get",
        "/payments/{paymentId}",
        ("200", "404"),
    ),
    OperationRequirement(
        "notification-service",
        "get",
        "/notifications",
        ("200",),
    ),
)
_FAILURE_REQUIREMENT: Final = OperationRequirement(
    "payment-service",
    "post",
    "/payments/mock-failures",
    ("201", "409"),
    True,
)


def contract_prerequisites(
    repository_root: Path,
    scenario: Scenario,
) -> tuple[str, ...]:
    documents = {
        requirement.service: _load_contract(repository_root, requirement.service)
        for requirement in (*_HAPPY_PATH_REQUIREMENTS, _FAILURE_REQUIREMENT)
    }
    requirements = list(_HAPPY_PATH_REQUIREMENTS)
    match scenario:
        case Scenario.HAPPY_PATH:
            pass
        case Scenario.PAYMENT_FAILURE | Scenario.LOW_STOCK_CONCURRENCY:
            requirements.append(_FAILURE_REQUIREMENT)
        case unreachable:
            assert_never(unreachable)
    missing = [
        requirement.label
        for requirement in requirements
        if not _operation_matches(documents[requirement.service], requirement)
    ]
    if scenario in {
        Scenario.PAYMENT_FAILURE,
        Scenario.LOW_STOCK_CONCURRENCY,
    } and _FAILURE_REQUIREMENT.label in missing:
        missing.append("OpenAPI payment failure request/response schema")
    return tuple(missing)


def _load_contract(repository_root: Path, service: str) -> JsonObject:
    path = (
        repository_root
        / "contracts"
        / "services"
        / service
        / "openapi.yaml"
    )
    try:
        decoded = yaml.safe_load(path.read_text(encoding="utf-8"))
        value = _JSON_ADAPTER.validate_python(decoded)
    except FileNotFoundError as error:
        raise RunnerStop(
            Verdict.BLOCKED,
            "API_CONTRACT_MISSING",
            (str(path.relative_to(repository_root)),),
        ) from error
    except (OSError, UnicodeError, yaml.YAMLError, ValidationError) as error:
        raise RunnerStop(
            Verdict.BLOCKED,
            "API_CONTRACT_INVALID",
            (str(path.relative_to(repository_root)),),
        ) from error
    if type(value) is not dict:
        raise RunnerStop(
            Verdict.BLOCKED,
            "API_CONTRACT_INVALID",
            (str(path.relative_to(repository_root)),),
        )
    return value


def _operation_matches(
    document: JsonObject,
    requirement: OperationRequirement,
) -> bool:
    paths = document.get("paths")
    if type(paths) is not dict:
        return False
    path_item = paths.get(requirement.path)
    if type(path_item) is not dict:
        return False
    operation = path_item.get(requirement.method)
    if type(operation) is not dict:
        return False
    responses = operation.get("responses")
    if type(responses) is not dict:
        return False
    if not all(status in responses for status in requirement.statuses):
        return False
    if not requirement.require_schemas:
        return True
    request_body = operation.get("requestBody")
    if not _declares_request_body_schema(request_body):
        return False
    return all(
        _declares_response_schema(responses[status])
        for status in requirement.statuses
    )


def _declares_schema(value: JsonValue) -> bool:
    if type(value) is not dict:
        return False
    reference = value.get("$ref")
    if type(reference) is str:
        return bool(reference.strip())
    schema = value.get("schema")
    return _is_meaningful_schema(schema)


def _is_meaningful_schema(value: JsonValue) -> bool:
    if type(value) is not dict or not value:
        return False
    reference = value.get("$ref")
    if type(reference) is str:
        return bool(reference.strip())
    for key in _SCHEMA_KEYS:
        if key not in value:
            continue
        candidate = value[key]
        if key == "const":
            return True
        if key == "type":
            if type(candidate) is str:
                return bool(candidate.strip())
            if type(candidate) is list:
                return bool(candidate)
            continue
        if type(candidate) is dict or type(candidate) is list:
            if candidate:
                return True
        elif key == "additionalProperties" and type(candidate) is bool:
            return True
    return False


def _declares_request_body_schema(value: JsonValue) -> bool:
    if type(value) is not dict:
        return False
    reference = value.get("$ref")
    if type(reference) is str:
        return bool(reference)
    content = value.get("content")
    if type(content) is not dict:
        return False
    media_type = content.get("application/json")
    if type(media_type) is not dict:
        return False
    return _declares_schema(media_type)


def _declares_response_schema(value: JsonValue) -> bool:
    if type(value) is not dict:
        return False
    reference = value.get("$ref")
    if type(reference) is str:
        return bool(reference)
    content = value.get("content")
    if type(content) is not dict:
        return False
    media_type = content.get("application/json")
    if type(media_type) is not dict:
        return False
    return _declares_schema(media_type)
