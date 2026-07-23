from __future__ import annotations

import json
from pathlib import Path


REPOSITORY_ROOT = Path(__file__).resolve().parents[4]


def test_internal_newman_fixture_remains_static_and_reserves_42_units() -> None:
    environment_path = (
        REPOSITORY_ROOT / "tests/e2e/newman/docker.postman_environment.json"
    )
    scenario_path = (
        REPOSITORY_ROOT
        / "tests/e2e/scenarios/06-sold-out-concurrency-flow.postman_collection.json"
    )

    environment = json.loads(environment_path.read_text(encoding="utf-8"))
    values = {entry["key"]: entry["value"] for entry in environment["values"]}
    scenario = json.loads(scenario_path.read_text(encoding="utf-8"))
    reservation_items = [
        item
        for item in scenario["item"]
        if item["name"].startswith("Create stock reservation order ")
    ]
    reserved_quantity = sum(
        json.loads(item["request"]["body"]["raw"])["quantity"]
        for item in reservation_items
    )

    assert "userId" in values
    assert "runId" not in values
    assert reserved_quantity == 42
