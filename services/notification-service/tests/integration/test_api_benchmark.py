from __future__ import annotations

from collections.abc import Callable, Iterator
import asyncio
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
import json
import math
import os
from pathlib import Path
import subprocess
import sys
import time
from typing import Any

from bson import ObjectId
from fastapi import FastAPI
from httpx import ASGITransport, AsyncClient
from motor.motor_asyncio import AsyncIOMotorClient, AsyncIOMotorDatabase
import pytest
from server.ids import deterministic_uuid_string

SERVICE_REPO_ROOT = Path(__file__).resolve().parents[4]
sys.path.insert(0, str(SERVICE_REPO_ROOT))

from tests.benchmarks.api_presets import ApiBenchmarkPreset, chunked, load_preset, user_id_for
from tests.benchmarks.query_analysis import explain_mongo_find, percentile_interpretation

import app.database as database
from app.routers.notifications import router as notification_router


SERVICE_NAME = "notification-service"
DB_NAME = "notification_db"


@dataclass(frozen=True)
class BenchmarkConfig:
    samples: int
    warmup: int
    artifact_dir: Path
    preset: ApiBenchmarkPreset


@dataclass(frozen=True)
class SeedTargets:
    notification_id: str
    normal_user_id: str
    heavy_user_id: str


@dataclass(frozen=True)
class EndpointCase:
    name: str
    method: str
    path: Callable[[int], str]
    status: int
    headers: Callable[[int], dict[str, str]] | None = None


def test_notification_api_benchmark_outputs_artifact(request: pytest.FixtureRequest) -> None:
    if not request.config.getoption("--run-notification-api-benchmark"):
        pytest.skip("use --run-notification-api-benchmark to run the isolated MongoDB benchmark")

    config = _benchmark_config(request)
    mongodb = pytest.importorskip("testcontainers.mongodb")
    docker = pytest.importorskip("docker")
    try:
        docker.from_env().ping()
    except Exception as exc:
        pytest.skip(f"Docker is not available for Testcontainers: {exc}")

    started_at = datetime.now(UTC)
    with mongodb.MongoDbContainer("mongo:7") as container:
        metrics, query_analysis = asyncio.run(_run_benchmark(container.get_connection_url(), config))
        artifact = _artifact(started_at, config, metrics, query_analysis)
        _write_artifact(config.artifact_dir, artifact, started_at)


def _benchmark_config(request: pytest.FixtureRequest) -> BenchmarkConfig:
    try:
        preset = load_preset(request.config.getoption("--notification-benchmark-preset"))
    except ValueError as exc:
        pytest.fail(str(exc))
    config = BenchmarkConfig(
        samples=request.config.getoption("--notification-benchmark-samples"),
        warmup=request.config.getoption("--notification-benchmark-warmup"),
        artifact_dir=Path(request.config.getoption("--notification-benchmark-artifact-dir")),
        preset=preset,
    )
    if config.samples < 1:
        pytest.fail("notification benchmark samples must be a positive integer")
    if config.warmup < 0:
        pytest.fail("notification benchmark warmup must be zero or greater")
    return config


def _benchmark_app() -> FastAPI:
    app = FastAPI(title=f"{SERVICE_NAME}-api-benchmark")
    app.include_router(notification_router)
    return app


async def _run_benchmark(connection_url: str, config: BenchmarkConfig) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    client = AsyncIOMotorClient(connection_url)
    database.client = client
    try:
        db = client[DB_NAME]
        await database.ensure_indexes()
        targets = await _seed_dataset(db, config)
        app = _benchmark_app()
        transport = ASGITransport(app=app)
        async with AsyncClient(transport=transport, base_url="http://testserver") as http_client:
            metrics = [
                await _measure_endpoint(http_client, endpoint, config)
                for endpoint in _benchmark_endpoints(targets)
            ]
        query_analysis = await _query_analysis(db, targets, config)
        return metrics, query_analysis
    finally:
        client.close()
        database.client = None


async def _seed_dataset(db: AsyncIOMotorDatabase, config: BenchmarkConfig) -> SeedTargets:
    await db["notifications"].delete_many({})
    await db["processed_events"].delete_many({})
    tables = config.preset.service_tables(SERVICE_NAME)
    now = datetime(2026, 7, 1, 12, 0, tzinfo=UTC)
    normal_user_id = user_id_for(SERVICE_NAME, "normal")
    heavy_user_id = user_id_for(SERVICE_NAME, "heavy")

    first_notification_id: str | None = None
    for docs in chunked(_notification_docs(config, now, normal_user_id, heavy_user_id)):
        if first_notification_id is None and docs:
            first_notification_id = str(docs[0]["_id"])
        await db["notifications"].insert_many(docs, ordered=False)
    for docs in chunked(_processed_event_docs(tables["processed_events"], now)):
        await db["processed_events"].insert_many(docs, ordered=False)
    if first_notification_id is None:
        pytest.fail("notification benchmark seed did not create a target notification")
    return SeedTargets(
        notification_id=first_notification_id,
        normal_user_id=normal_user_id,
        heavy_user_id=heavy_user_id,
    )


def _notification_docs(
    config: BenchmarkConfig,
    now: datetime,
    normal_user_id: str,
    heavy_user_id: str,
) -> Iterator[dict[str, Any]]:
    total = config.preset.service_tables(SERVICE_NAME)["notifications"]
    heavy_rows = min(max(50, int(total * config.preset.user_distribution["heavy"])), total)
    normal_rows = min(max(25, config.samples), max(total - heavy_rows, 0))
    for index in range(total):
        if index < heavy_rows:
            user_id = heavy_user_id
        elif index < heavy_rows + normal_rows:
            user_id = normal_user_id
        else:
            user_id = _notification_user_id(index, total, config)
        yield {
            "_id": ObjectId(),
            "user_id": user_id,
            "type": _notification_type(index),
            "message": f"Benchmark notification {index:06d}",
            "status": "CREATED",
            "source_id": _benchmark_uuid("notification-source", index),
            "metadata": {
                "concert_id": _benchmark_uuid("concert", index % config.preset.catalog["concerts"]),
                "retention_days": config.preset.notification_retention_days,
            },
            "created_at": now - timedelta(seconds=index),
        }


def _processed_event_docs(total: int, now: datetime) -> Iterator[dict[str, Any]]:
    for index in range(total):
        yield {
            "event_id": _benchmark_uuid("business-event", index),
            "notification_id": str(ObjectId()),
            "created_at": now - timedelta(seconds=index),
        }


def _notification_user_id(index: int, total: int, config: BenchmarkConfig) -> str:
    distribution = config.preset.user_distribution
    heavy_cutoff = int(total * distribution["heavy"])
    repeat_cutoff = heavy_cutoff + int(total * distribution["repeat"])
    if index < heavy_cutoff:
        return user_id_for(SERVICE_NAME, "heavy")
    if index < repeat_cutoff:
        return user_id_for(SERVICE_NAME, "repeat", index % max(1, int(total * distribution["repeat"] / 8)))
    return user_id_for(SERVICE_NAME, "normal", index % max(1, min(config.preset.active_users, total)))


def _notification_type(index: int) -> str:
    types = ("reservation-created", "reservation-expired", "payment-approved", "payment-failed", "ticket-issued")
    return types[index % len(types)]


def _benchmark_uuid(*parts: object) -> str:
    return deterministic_uuid_string(SERVICE_NAME, *parts)


def _benchmark_endpoints(targets: SeedTargets) -> list[EndpointCase]:
    return [
        EndpointCase(
            name="list-notifications-normal-first-page",
            method="GET",
            path=lambda _: "/notifications?limit=20",
            status=200,
            headers=lambda _: _user_headers(targets.normal_user_id),
        ),
        EndpointCase(
            name="list-notifications-heavy-first-page",
            method="GET",
            path=lambda _: "/notifications?limit=20",
            status=200,
            headers=lambda _: _user_headers(targets.heavy_user_id),
        ),
        EndpointCase(
            name="get-notification",
            method="GET",
            path=lambda _: f"/notifications/{targets.notification_id}",
            status=200,
            headers=lambda _: _user_headers(targets.heavy_user_id),
        ),
    ]


def _user_headers(user_id: str) -> dict[str, str]:
    return {"X-User-Id": user_id, "X-User-Role": "USER"}


async def _measure_endpoint(client: AsyncClient, endpoint: EndpointCase, config: BenchmarkConfig) -> dict[str, Any]:
    durations: list[float] = []
    total = config.warmup + config.samples
    for iteration in range(total):
        started = time.perf_counter_ns()
        response = await client.request(
            endpoint.method,
            endpoint.path(iteration),
            headers=endpoint.headers(iteration) if endpoint.headers else None,
        )
        elapsed_ms = (time.perf_counter_ns() - started) / 1_000_000
        if response.status_code != endpoint.status:
            pytest.fail(f"{endpoint.name} returned HTTP {response.status_code}: {response.text}")
        if endpoint.name.startswith("list-notifications"):
            _assert_first_page_response(endpoint.name, response.json())
        if iteration >= config.warmup:
            durations.append(elapsed_ms)
    return _metric(endpoint, durations, config)


def _metric(endpoint: EndpointCase, durations: list[float], config: BenchmarkConfig) -> dict[str, Any]:
    return {
        "serviceName": SERVICE_NAME,
        "datasetPreset": config.preset.name,
        "seedRows": config.preset.service_tables(SERVICE_NAME),
        "endpoint": endpoint.name,
        "method": endpoint.method,
        "status": endpoint.status,
        "samples": len(durations),
        "warmup": config.warmup,
        "minMs": round(min(durations), 3),
        "p50Ms": round(_percentile(durations, 50), 3),
        "p95Ms": round(_percentile(durations, 95), 3),
        "p99Ms": round(_percentile(durations, 99), 3),
        "maxMs": round(max(durations), 3),
    }


def _assert_first_page_response(endpoint_name: str, body: dict[str, Any]) -> None:
    if len(body["items"]) != 20:
        pytest.fail(f"{endpoint_name} returned {len(body['items'])} items, expected first page size 20")
    if body["page"] != {"nextCursor": body["items"][-1]["id"], "hasMore": True, "limit": 20}:
        pytest.fail(f"{endpoint_name} returned unexpected page metadata: {body['page']}")


async def _query_analysis(
    db: AsyncIOMotorDatabase,
    targets: SeedTargets,
    config: BenchmarkConfig,
) -> list[dict[str, Any]]:
    total = config.preset.service_tables(SERVICE_NAME)["notifications"]
    heavy_rows = min(max(50, int(total * config.preset.user_distribution["heavy"])), total)
    normal_rows = min(max(25, config.samples), max(total - heavy_rows, 0))
    return [
        {
            "endpoint": "list-notifications-normal-first-page",
            "queries": [
                await explain_mongo_find(
                    db["notifications"],
                    label="notifications-by-normal-user-sort-id-desc",
                    filter_query={"user_id": targets.normal_user_id},
                    sort={"_id": -1},
                    limit=21,
                    query_shape='db.notifications.find({user_id}).sort({_id: -1}).limit(21)',
                    index_decision=(
                        "현재 (user_id, _id desc) 복합 인덱스를 사용한다. "
                        "첫 페이지는 API limit 20에 hasMore 판단용 1건을 더해 21건만 확인한다."
                    ),
                    data_analysis=(
                        f"normal 사용자 보유 알림은 약 {normal_rows:,}건이지만 첫 페이지 비용은 page size에 가깝다. "
                        "heavy/normal 차이는 전체 알림함 크기보다 첫 페이지 조회와 응답 직렬화 비용으로 비교한다."
                    ),
                )
            ],
            "sampleInterpretation": percentile_interpretation(config.samples),
        },
        {
            "endpoint": "list-notifications-heavy-first-page",
            "queries": [
                await explain_mongo_find(
                    db["notifications"],
                    label="notifications-by-heavy-user-sort-id-desc",
                    filter_query={"user_id": targets.heavy_user_id},
                    sort={"_id": -1},
                    limit=21,
                    query_shape='db.notifications.find({user_id}).sort({_id: -1}).limit(21)',
                    index_decision=(
                        "API가 limit + 1 cursor pagination으로 바뀌었으므로 "
                        "(user_id, _id desc) 복합 인덱스가 첫 페이지와 다음 페이지 조회를 직접 지원한다."
                    ),
                    data_analysis=(
                        f"large preset notifications={total:,}, heavy 비율={config.preset.user_distribution['heavy']:.0%}라 "
                        f"헤비 사용자 1명에게 약 {heavy_rows:,}건이 몰려도 첫 응답은 20건만 반환한다. "
                        "pagination 적용 후에는 보유 알림 전체 수가 아니라 page size가 목록 API 비용을 좌우한다."
                    ),
                )
            ],
            "sampleInterpretation": percentile_interpretation(config.samples),
        },
        {
            "endpoint": "get-notification",
            "queries": [
                await explain_mongo_find(
                    db["notifications"],
                    label="notification-by-id",
                    filter_query={"_id": ObjectId(targets.notification_id)},
                    sort=None,
                    query_shape="db.notifications.find({_id})",
                    index_decision="MongoDB 기본 _id 인덱스로 충분하다. 별도 인덱스 추가 대상이 아니다.",
                    data_analysis="단일 문서 조회라 전체 notifications 규모보다 _id lookup과 응답 직렬화 비용의 영향을 받는다.",
                )
            ],
            "sampleInterpretation": percentile_interpretation(config.samples),
        },
    ]


def _percentile(values: list[float], percentile: int) -> float:
    ordered = sorted(values)
    index = max(math.ceil(len(ordered) * percentile / 100) - 1, 0)
    return ordered[index]


def _artifact(
    started_at: datetime,
    config: BenchmarkConfig,
    metrics: list[dict[str, Any]],
    query_analysis: list[dict[str, Any]],
) -> dict[str, Any]:
    return {
        "generatedAt": started_at.isoformat(),
        "finishedAt": datetime.now(UTC).isoformat(),
        "serviceName": SERVICE_NAME,
        "datasetPreset": config.preset.name,
        "datasetPresetPath": str(config.preset.path.relative_to(SERVICE_REPO_ROOT)),
        "seed": config.preset.seed_summary(SERVICE_NAME),
        "service": _git_info(SERVICE_REPO_ROOT),
        "benchmark": {
            "samplesPerEndpoint": config.samples,
            "warmup": config.warmup,
            "endpoints": metrics,
        },
        "queryAnalysis": query_analysis,
        "constraints": [
            "FastAPI TestClient measures router and service code paths together.",
            "MongoDB runs in testcontainers mongo:7 and is removed when the test ends.",
            "Seed/setup work runs before the measured loop.",
            "This benchmark measures one API request at a time against a seeded database; it is not a concurrent load test.",
        ],
    }


def _write_artifact(directory: Path, artifact: dict[str, Any], started_at: datetime) -> None:
    directory.mkdir(parents=True, exist_ok=True)
    timestamp = started_at.strftime("%Y%m%dT%H%M%SZ")
    output = json.dumps(artifact, indent=2, sort_keys=True)
    (directory / f"{timestamp}.json").write_text(output + "\n", encoding="utf-8")
    (directory / "latest.json").write_text(output + "\n", encoding="utf-8")


def _git_info(repo_root: Path) -> dict[str, Any]:
    head = subprocess.run(
        ["git", "-C", str(repo_root), "rev-parse", "--short", "HEAD"],
        check=True,
        capture_output=True,
        text=True,
    )
    status = subprocess.run(
        ["git", "-C", str(repo_root), "status", "--short"],
        check=True,
        capture_output=True,
        text=True,
    )
    return {
        "head": head.stdout.strip(),
        "dirty": bool(status.stdout.strip()),
        "statusShort": status.stdout.splitlines(),
        "servicePath": os.getcwd(),
    }
