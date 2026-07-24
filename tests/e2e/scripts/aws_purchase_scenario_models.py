from __future__ import annotations

from dataclasses import dataclass, field
from enum import StrEnum, unique
from hashlib import sha256
from pathlib import Path
from typing import NewType

type JsonValue = (
    None | bool | int | float | str | list["JsonValue"] | dict[str, "JsonValue"]
)
type JsonObject = dict[str, JsonValue]

BearerToken = NewType("BearerToken", str)
FixtureId = NewType("FixtureId", str)


@unique
class Mode(StrEnum):
    DRY_RUN = "dry-run"
    PREFLIGHT = "preflight"
    EXECUTE = "execute"


@unique
class Scenario(StrEnum):
    HAPPY_PATH = "04"
    PAYMENT_FAILURE = "05"
    LOW_STOCK_CONCURRENCY = "06"


@unique
class Verdict(StrEnum):
    READY = "READY"
    PASS = "PASS"
    FAIL = "FAIL"
    BLOCKED = "BLOCKED"


@dataclass(frozen=True, slots=True)
class Credentials:
    email: str = field(repr=False)
    password: str = field(repr=False)


@dataclass(frozen=True, slots=True)
class Fixture:
    run_id: str
    drop_id: FixtureId
    product_id: FixtureId
    initial_stock: int
    subject_refs: tuple[str, str]


@dataclass(frozen=True, slots=True)
class Bounds:
    max_attempts: int
    poll_attempts: int
    poll_interval_seconds: float
    timeout_seconds: float


@dataclass(frozen=True, slots=True)
class Config:
    environment: str
    mode: Mode
    scenario: Scenario
    base_url: str
    ingress_fingerprint: str
    run_id: str
    output_path: Path
    fixture: Fixture
    credentials: Credentials
    second_credentials: Credentials | None
    bounds: Bounds
    live_fixture_verified: bool


@dataclass(frozen=True, slots=True)
class Stage:
    name: str
    method: str
    status_code: int
    attempts: int

    def as_json(self) -> JsonObject:
        return {
            "name": self.name,
            "method": self.method,
            "status_code": self.status_code,
            "attempts": self.attempts,
        }


@dataclass(frozen=True, slots=True)
class ScenarioSummary:
    order_status: str
    payment_status: str
    notification_count: int
    inventory_delta: int
    order_fingerprint: str
    payment_fingerprint: str

    def as_json(self) -> JsonObject:
        return {
            "order_status": self.order_status,
            "payment_status": self.payment_status,
            "notification_count": self.notification_count,
            "inventory_delta": self.inventory_delta,
            "order_fingerprint": self.order_fingerprint,
            "payment_fingerprint": self.payment_fingerprint,
        }


@dataclass(frozen=True, slots=True)
class Report:
    run_id: str
    environment: str
    mode: str
    scenario: str
    verdict: Verdict
    reason_code: str
    ingress_fingerprint: str
    fixture: Fixture | None
    requests_sent: int
    purchase_write_requests_sent: int
    stages: tuple[Stage, ...]
    prerequisites: tuple[str, ...] = ()
    summary: ScenarioSummary | None = None

    def as_json(self) -> JsonObject:
        result: JsonObject = {
            "schema_version": 1,
            "run_id": self.run_id,
            "environment": self.environment,
            "mode": self.mode,
            "scenario": self.scenario,
            "verdict": self.verdict.value,
            "reason_code": self.reason_code,
            "ingress_fingerprint": self.ingress_fingerprint,
            "requests_sent": self.requests_sent,
            "purchase_write_requests_sent": self.purchase_write_requests_sent,
            "stages": [stage.as_json() for stage in self.stages],
            "prerequisites": list(self.prerequisites),
        }
        if self.fixture is not None:
            result["fixture"] = {
                "dedicated": True,
                "initial_stock": self.fixture.initial_stock,
                "drop_fingerprint": fingerprint(self.fixture.drop_id),
                "product_fingerprint": fingerprint(self.fixture.product_id),
                "subject_fingerprints": [
                    fingerprint(subject) for subject in self.fixture.subject_refs
                ],
            }
        if self.summary is not None:
            result["summary"] = self.summary.as_json()
        return result


@dataclass(frozen=True, slots=True)
class RunnerStop(Exception):
    verdict: Verdict
    reason_code: str
    prerequisites: tuple[str, ...] = ()

    def __str__(self) -> str:
        return self.reason_code


def fingerprint(value: str) -> str:
    return sha256(value.encode("utf-8")).hexdigest()[:16]
