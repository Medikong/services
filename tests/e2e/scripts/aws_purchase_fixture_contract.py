from __future__ import annotations

import re
from dataclasses import dataclass
from enum import StrEnum, unique
from hashlib import sha256
from typing import Final


type _JsonValue = (
    None | bool | int | float | str | list["_JsonValue"] | dict[str, "_JsonValue"]
)
type _JsonObject = dict[str, _JsonValue]

_RUN_ID_PATTERN: Final = re.compile(r"aws-purchase-[0-9]{8}T[0-9]{6}Z-[a-f0-9]{8}")
_OPAQUE_REF_PATTERN: Final = re.compile(r"opaque-[a-z0-9][a-z0-9-]{0,62}")
_TOP_LEVEL_FIELDS: Final = frozenset(
    {
        "schema_version",
        "run_id",
        "users",
        "fixture",
        "active_records",
        "retention",
    }
)
_USER_FIELDS: Final = frozenset(
    {"subject_ref", "credential_ref", "credential_status", "role"}
)
_FIXTURE_FIELDS: Final = frozenset(
    {"namespace", "drop_ref", "product_ref", "dedicated", "stock"}
)
_RETENTION_FIELDS: Final = frozenset({"days", "cleanup", "automatic_compensation"})


@unique
class _ReasonCode(StrEnum):
    NONE = "NONE"
    MANIFEST_MISSING = "MANIFEST_MISSING"
    MANIFEST_INVALID = "MANIFEST_INVALID"
    RUN_ID_INVALID = "RUN_ID_INVALID"
    USER_COUNT_INVALID = "USER_COUNT_INVALID"
    CREDENTIALS_MISSING = "CREDENTIALS_MISSING"
    DUPLICATE_USERS = "DUPLICATE_USERS"
    FIXTURE_SCOPE_INVALID = "FIXTURE_SCOPE_INVALID"
    FIXTURE_STOCK_INVALID = "FIXTURE_STOCK_INVALID"
    ACTIVE_RECORDS_PRESENT = "ACTIVE_RECORDS_PRESENT"
    RETENTION_POLICY_INVALID = "RETENTION_POLICY_INVALID"
    RUN_ID_REUSED = "RUN_ID_REUSED"
    LIVE_PROVISIONING_UNAVAILABLE = "LIVE_PROVISIONING_UNAVAILABLE"
    STATE_WRITE_BLOCKED = "STATE_WRITE_BLOCKED"
    ARTIFACT_WRITE_BLOCKED = "ARTIFACT_WRITE_BLOCKED"
    INTERRUPTED = "INTERRUPTED"
    INTERNAL_ERROR = "INTERNAL_ERROR"


@dataclass(frozen=True, slots=True)
class _Refusal(Exception):
    reason: _ReasonCode

    def __str__(self) -> str:
        return self.reason.value


@dataclass(frozen=True, slots=True)
class _OperationalBlock(Exception):
    reason: _ReasonCode

    def __str__(self) -> str:
        return self.reason.value


@dataclass(frozen=True, slots=True)
class _User:
    subject_ref: str
    credential_ref: str


@dataclass(frozen=True, slots=True)
class _Fixture:
    namespace: str
    drop_ref: str
    product_ref: str
    stock: int


@dataclass(frozen=True, slots=True)
class _Manifest:
    run_id: str
    users: tuple[_User, _User]
    fixture: _Fixture


def _mapping(value: _JsonValue) -> dict[str, _JsonValue]:
    if type(value) is not dict:
        raise _Refusal(_ReasonCode.MANIFEST_INVALID)
    return value


def _safe_ref(value: _JsonValue) -> str:
    if type(value) is not str or _OPAQUE_REF_PATTERN.fullmatch(value) is None:
        raise _Refusal(_ReasonCode.MANIFEST_INVALID)
    return value


def _parse_user(value: _JsonValue) -> _User:
    fields = _mapping(value)
    if "credential_ref" not in fields or "credential_status" not in fields:
        raise _Refusal(_ReasonCode.CREDENTIALS_MISSING)
    if set(fields) != _USER_FIELDS:
        raise _Refusal(_ReasonCode.MANIFEST_INVALID)
    if fields["credential_status"] != "present":
        raise _Refusal(_ReasonCode.CREDENTIALS_MISSING)
    if fields["role"] != "customer":
        raise _Refusal(_ReasonCode.MANIFEST_INVALID)
    return _User(
        subject_ref=_safe_ref(fields["subject_ref"]),
        credential_ref=_safe_ref(fields["credential_ref"]),
    )


def _parse_fixture(value: _JsonValue, run_id: str) -> _Fixture:
    fields = _mapping(value)
    if set(fields) != _FIXTURE_FIELDS:
        raise _Refusal(_ReasonCode.MANIFEST_INVALID)
    namespace = fields["namespace"]
    if namespace != run_id or fields["dedicated"] is not True:
        raise _Refusal(_ReasonCode.FIXTURE_SCOPE_INVALID)
    stock = fields["stock"]
    if type(stock) is not int or stock != 42:
        raise _Refusal(_ReasonCode.FIXTURE_STOCK_INVALID)
    drop_ref = _safe_ref(fields["drop_ref"])
    product_ref = _safe_ref(fields["product_ref"])
    if drop_ref == product_ref:
        raise _Refusal(_ReasonCode.FIXTURE_SCOPE_INVALID)
    return _Fixture(
        namespace=run_id,
        drop_ref=drop_ref,
        product_ref=product_ref,
        stock=stock,
    )


def _verify_retention(value: _JsonValue) -> None:
    fields = _mapping(value)
    if set(fields) != _RETENTION_FIELDS:
        raise _Refusal(_ReasonCode.RETENTION_POLICY_INVALID)
    if (
        fields["days"] != 30
        or fields["cleanup"] != "retention_only"
        or fields["automatic_compensation"] is not False
    ):
        raise _Refusal(_ReasonCode.RETENTION_POLICY_INVALID)


def _parse_manifest(value: _JsonValue) -> _Manifest:
    fields = _mapping(value)
    if set(fields) != _TOP_LEVEL_FIELDS or fields["schema_version"] != 1:
        raise _Refusal(_ReasonCode.MANIFEST_INVALID)
    run_id_value = fields["run_id"]
    if type(run_id_value) is not str or _RUN_ID_PATTERN.fullmatch(run_id_value) is None:
        raise _Refusal(_ReasonCode.RUN_ID_INVALID)
    run_id = run_id_value
    users_value = fields["users"]
    if type(users_value) is not list or len(users_value) != 2:
        raise _Refusal(_ReasonCode.USER_COUNT_INVALID)
    first, second = (_parse_user(user) for user in users_value)
    if (
        first.subject_ref == second.subject_ref
        or first.credential_ref == second.credential_ref
    ):
        raise _Refusal(_ReasonCode.DUPLICATE_USERS)
    active_records = fields["active_records"]
    if type(active_records) is not list:
        raise _Refusal(_ReasonCode.MANIFEST_INVALID)
    if active_records:
        raise _Refusal(_ReasonCode.ACTIVE_RECORDS_PRESENT)
    fixture = _parse_fixture(fields["fixture"], run_id)
    _verify_retention(fields["retention"])
    return _Manifest(run_id=run_id, users=(first, second), fixture=fixture)


def _fingerprint(value: str) -> str:
    return sha256(value.encode("utf-8")).hexdigest()[:16]


def _artifact(
    verdict: str,
    reason: _ReasonCode,
    manifest: _Manifest | None = None,
) -> _JsonObject:
    result: _JsonObject = {
        "schema_version": 1,
        "verdict": verdict,
        "reason_code": reason.value,
        "api_traffic_allowed": False,
        "runtime_provisioning": {
            "status": "BLOCKED",
            "reason_code": _ReasonCode.LIVE_PROVISIONING_UNAVAILABLE.value,
        },
    }
    if manifest is None:
        return result
    result["run_id"] = manifest.run_id
    result["users"] = {
        "count": 2,
        "subject_fingerprints": [
            _fingerprint(user.subject_ref) for user in manifest.users
        ],
        "credentials": "REFERENCES_PRESENT",
    }
    result["fixture"] = {
        "namespace_fingerprint": _fingerprint(manifest.fixture.namespace),
        "drop_fingerprint": _fingerprint(manifest.fixture.drop_ref),
        "product_fingerprint": _fingerprint(manifest.fixture.product_ref),
        "dedicated": True,
        "stock": manifest.fixture.stock,
    }
    result["retention"] = {
        "days": 30,
        "cleanup": "retention_only",
        "automatic_compensation": False,
        "shared_database_reset": False,
    }
    return result
