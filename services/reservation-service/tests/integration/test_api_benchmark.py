from __future__ import annotations

from collections.abc import Callable, Iterator
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

from fastapi import FastAPI
from fastapi.testclient import TestClient
import pytest
from sqlalchemy import create_engine, insert, text
from sqlalchemy.orm import Session, sessionmaker
from server.ids import deterministic_uuid_string

SERVICE_REPO_ROOT = Path(__file__).resolve().parents[4]
sys.path.insert(0, str(SERVICE_REPO_ROOT))

from tests.benchmarks.api_presets import ApiBenchmarkPreset, chunked, load_preset, status_for_index, user_id_for
from tests.benchmarks.query_analysis import explain_postgres_sql, percentile_interpretation

from app import entities as model
from app.database import Base
from app.dependencies import get_db
from app.exceptions import register_exception_handlers
from app.kafka import get_kafka_producer
from app.routers import router as reservation_router


SERVICE_NAME = "reservation-service"


def uuid_id(*parts: object) -> str:
    return deterministic_uuid_string(SERVICE_NAME, *parts)


@dataclass(frozen=True)
class BenchmarkConfig:
    samples: int
    warmup: int
    artifact_dir: Path
    preset: ApiBenchmarkPreset


@dataclass(frozen=True)
class SeedTargets:
    get_reservation_id: str
    list_normal_user_id: str
    list_heavy_user_id: str
    concert_id: str
    showtime_id: str
    cancel_ids: list[str]
    expire_ids: list[str]


@dataclass(frozen=True)
class EndpointCase:
    name: str
    method: str
    path: Callable[[int], str]
    status: int
    json_body: Callable[[int], dict[str, Any]] | None = None
    headers: Callable[[int], dict[str, str]] | None = None


def test_reservation_api_benchmark_outputs_artifact(request: pytest.FixtureRequest) -> None:
    if not request.config.getoption("--run-reservation-api-benchmark"):
        pytest.skip("use --run-reservation-api-benchmark to run the isolated PostgreSQL benchmark")

    config = _benchmark_config(request)
    postgres = pytest.importorskip("testcontainers.postgres")
    docker = pytest.importorskip("docker")
    try:
        docker.from_env().ping()
    except Exception as exc:
        pytest.skip(f"Docker is not available for Testcontainers: {exc}")

    started_at = datetime.now(UTC)
    with postgres.PostgresContainer("postgres:16-alpine") as container:
        engine = create_engine(container.get_connection_url(driver="psycopg"))
        try:
            Base.metadata.drop_all(engine)
            Base.metadata.create_all(engine)
            factory = sessionmaker(bind=engine)
            with factory() as session:
                targets = _seed_dataset(session, config)
                session.commit()
            with factory() as session:
                session.execute(text("ANALYZE"))
                session.commit()
            app = _benchmark_app(factory)
            with TestClient(app) as client:
                metrics = [
                    _measure_endpoint(client, endpoint, config)
                    for endpoint in _benchmark_endpoints(targets, config)
                ]
            with factory() as session:
                query_analysis = _query_analysis(session, targets, config)
            artifact = _artifact(started_at, config, metrics, query_analysis)
            _write_artifact(config.artifact_dir, artifact, started_at)
        finally:
            engine.dispose()


def _benchmark_config(request: pytest.FixtureRequest) -> BenchmarkConfig:
    try:
        preset = load_preset(request.config.getoption("--reservation-benchmark-preset"))
    except ValueError as exc:
        pytest.fail(str(exc))
    config = BenchmarkConfig(
        samples=request.config.getoption("--reservation-benchmark-samples"),
        warmup=request.config.getoption("--reservation-benchmark-warmup"),
        artifact_dir=Path(request.config.getoption("--reservation-benchmark-artifact-dir")),
        preset=preset,
    )
    if config.samples < 1:
        pytest.fail("reservation benchmark samples must be a positive integer")
    if config.warmup < 0:
        pytest.fail("reservation benchmark warmup must be zero or greater")
    return config


def _benchmark_app(factory: sessionmaker[Session]) -> FastAPI:
    app = FastAPI(title=f"{SERVICE_NAME}-api-benchmark")
    register_exception_handlers(app)
    app.include_router(reservation_router)
    app.state.kafka_producer = None

    def override_get_db() -> Iterator[Session]:
        db = factory()
        try:
            yield db
        finally:
            db.close()

    app.dependency_overrides[get_db] = override_get_db
    app.dependency_overrides[get_kafka_producer] = lambda: None
    return app


def _seed_dataset(session: Session, config: BenchmarkConfig) -> SeedTargets:
    now = datetime(2026, 7, 1, 12, 0, tzinfo=UTC)
    tables = config.preset.service_tables(SERVICE_NAME)
    total_reservations = tables["reservations"]
    measured_total = config.samples + config.warmup
    if measured_total * 2 > total_reservations:
        pytest.fail("reservation benchmark preset must have enough rows for cancel and expire measurements")

    normal_user_id = user_id_for(SERVICE_NAME, "normal")
    heavy_user_id = user_id_for(SERVICE_NAME, "heavy")
    concert_id = uuid_id("concert", "bench", 0)
    showtime_id = uuid_id("showtime", "bench", 0)
    cancel_ids = [uuid_id("reservation", "cancel", index) for index in range(measured_total)]
    expire_ids = [uuid_id("reservation", "expire", index) for index in range(measured_total)]

    for rows in chunked(_reservation_rows(config, now, cancel_ids, expire_ids, normal_user_id, heavy_user_id)):
        session.execute(insert(model.Reservation), rows)
    session.execute(insert(model.SalesState), list(_sales_rows(tables["sales_states"], now)))
    session.execute(insert(model.QueuePolicy), list(_queue_policy_rows(tables["queue_policies"])))
    session.execute(insert(model.TrafficPolicy), list(_traffic_policy_rows(tables["traffic_policies"])))
    return SeedTargets(
        get_reservation_id=uuid_id("reservation", "seed", 0),
        list_normal_user_id=normal_user_id,
        list_heavy_user_id=heavy_user_id,
        concert_id=concert_id,
        showtime_id=showtime_id,
        cancel_ids=cancel_ids,
        expire_ids=expire_ids,
    )


def _reservation_rows(
    config: BenchmarkConfig,
    now: datetime,
    cancel_ids: list[str],
    expire_ids: list[str],
    normal_user_id: str,
    heavy_user_id: str,
) -> Iterator[dict[str, Any]]:
    counts = dict(config.preset.reservation_status_counts)
    measured_total = config.samples + config.warmup
    if counts["canceled"] + counts["expired"] < measured_total * 2:
        pytest.fail("reservationStatusCounts must leave enough abandoned rows for cancel and expire endpoints")
    counts["canceled"] -= measured_total
    counts["expired"] -= measured_total
    total = config.preset.service_tables(SERVICE_NAME)["reservations"]

    for index, reservation_id in enumerate(cancel_ids):
        yield _reservation_row(
            reservation_id,
            user_id_for(SERVICE_NAME, "cancel"),
            uuid_id("concert", "cancel", index),
            uuid_id("showtime", "cancel", index),
            uuid_id("performance", "cancel", index),
            uuid_id("seat", "cancel", index),
            "pending",
            now + timedelta(seconds=index),
        )
    for index, reservation_id in enumerate(expire_ids):
        yield _reservation_row(
            reservation_id,
            user_id_for(SERVICE_NAME, "expire"),
            uuid_id("concert", "expire", index),
            uuid_id("showtime", "expire", index),
            uuid_id("performance", "expire", index),
            uuid_id("seat", "expire", index),
            "pending",
            now + timedelta(minutes=1, seconds=index),
        )

    remaining = total - len(cancel_ids) - len(expire_ids)
    heavy_rows = min(max(25, int(total * config.preset.user_distribution["heavy"])), remaining)
    normal_rows = min(max(25, config.samples), max(remaining - heavy_rows, 0))
    for offset in range(remaining):
        seed_index = offset + len(cancel_ids) + len(expire_ids)
        if offset < heavy_rows:
            user_id = heavy_user_id
        elif offset < heavy_rows + normal_rows:
            user_id = normal_user_id
        else:
            user_id = _distributed_reservation_user_id(seed_index, total, config)
        status = status_for_index(offset, counts)
        yield _reservation_row(
            uuid_id("reservation", "seed", offset),
            user_id,
            uuid_id("concert", "bench", offset % config.preset.catalog["concerts"]),
            uuid_id("showtime", "bench", offset % config.preset.catalog["showtimes"]),
            uuid_id("performance", "bench", offset % config.preset.catalog["showtimes"]),
            uuid_id("seat", "bench", offset),
            status,
            now - timedelta(seconds=offset),
        )


def _distributed_reservation_user_id(index: int, total: int, config: BenchmarkConfig) -> str:
    distribution = config.preset.user_distribution
    heavy_cutoff = int(total * distribution["heavy"])
    repeat_cutoff = heavy_cutoff + int(total * distribution["repeat"])
    if index < heavy_cutoff:
        return user_id_for(SERVICE_NAME, "heavy")
    if index < repeat_cutoff:
        return user_id_for(SERVICE_NAME, "repeat", index % max(1, int(total * distribution["repeat"] / 8)))
    return user_id_for(SERVICE_NAME, "normal", index % max(1, min(config.preset.active_users, total)))


def _reservation_row(
    reservation_id: str,
    user_id: str,
    concert_id: str,
    showtime_id: str,
    performance_id: str,
    seat_id: str,
    status: str,
    created_at: datetime,
) -> dict[str, Any]:
    return {
        "id": reservation_id,
        "user_id": user_id,
        "concert_id": concert_id,
        "showtime_id": showtime_id,
        "performance_id": performance_id,
        "seat_id": seat_id,
        "status": status,
        "active_seat_key": f"{performance_id}:{seat_id}" if status in {"pending", "paid"} else None,
        "expires_at": created_at + timedelta(minutes=5),
        "created_at": created_at,
        "updated_at": None,
    }


def _sales_rows(count: int, now: datetime) -> Iterator[dict[str, Any]]:
    for index in range(count):
        status = "paused" if index % 2 else "open"
        yield {
            "concert_id": uuid_id("concert", "bench", index),
            "sales_status": status,
            "total_seats": 2100,
            "updated_at": now,
        }


def _queue_policy_rows(count: int) -> Iterator[dict[str, Any]]:
    for index in range(count):
        yield {
            "concert_id": uuid_id("concert", "bench", index),
            "enabled": index % 2 == 0,
            "max_entrants_per_minute": 1000,
            "waiting_room_url": "https://queue.example.com",
        }


def _traffic_policy_rows(count: int) -> Iterator[dict[str, Any]]:
    for index in range(count):
        yield {
            "concert_id": uuid_id("concert", "bench", index),
            "macro_protection_enabled": True,
            "max_requests_per_user_per_minute": 60,
            "block_suspicious_traffic": True,
        }


def _benchmark_endpoints(targets: SeedTargets, config: BenchmarkConfig) -> list[EndpointCase]:
    measured_total = config.samples + config.warmup
    policy_count = config.preset.service_tables(SERVICE_NAME)["sales_states"]

    return [
        EndpointCase(
            name="create-reservation",
            method="POST",
            path=lambda _: "/reservations",
            status=201,
            json_body=lambda index: {
                "concertId": uuid_id("concert", "create", index),
                "showtimeId": uuid_id("showtime", "create", index),
                "performanceId": uuid_id("performance", "create", index),
                "seatId": uuid_id("seat", "create", index),
            },
            headers=lambda index: {"X-User-Id": f"bench-user-create-{index:06d}"},
        ),
        EndpointCase(
            name="list-my-reservations-normal-first-page",
            method="GET",
            path=lambda _: "/reservations/me?limit=20",
            status=200,
            headers=lambda _: {"X-User-Id": targets.list_normal_user_id},
        ),
        EndpointCase(
            name="list-my-reservations-heavy-first-page",
            method="GET",
            path=lambda _: "/reservations/me?limit=20",
            status=200,
            headers=lambda _: {"X-User-Id": targets.list_heavy_user_id},
        ),
        EndpointCase(
            name="get-reservation",
            method="GET",
            path=lambda _: f"/reservations/{targets.get_reservation_id}",
            status=200,
        ),
        EndpointCase(
            name="cancel-reservation",
            method="POST",
            path=lambda index: f"/reservations/{targets.cancel_ids[index % measured_total]}/cancel",
            status=200,
        ),
        EndpointCase(
            name="expire-reservation",
            method="POST",
            path=lambda index: f"/reservations/{targets.expire_ids[index % measured_total]}/expire",
            status=200,
        ),
        EndpointCase(
            name="admin-start-sales",
            method="POST",
            path=lambda index: f"/admin/concerts/{uuid_id('concert', 'start', index)}/sales/start",
            status=200,
        ),
        EndpointCase(
            name="admin-pause-sales",
            method="POST",
            path=lambda index: f"/admin/concerts/{uuid_id('concert', 'bench', (index % policy_count) * 2)}/sales/pause",
            status=200,
        ),
        EndpointCase(
            name="admin-resume-sales",
            method="POST",
            path=lambda index: f"/admin/concerts/{uuid_id('concert', 'bench', ((index % policy_count) * 2 + 1) % policy_count)}/sales/resume",
            status=200,
        ),
        EndpointCase(
            name="admin-get-sales",
            method="GET",
            path=lambda _: f"/admin/concerts/{targets.concert_id}/sales",
            status=200,
        ),
        EndpointCase(
            name="provider-concert-sales",
            method="GET",
            path=lambda _: f"/provider/concerts/{targets.concert_id}/sales",
            status=200,
        ),
        EndpointCase(
            name="provider-showtime-sales",
            method="GET",
            path=lambda _: f"/provider/showtimes/{targets.showtime_id}/sales",
            status=200,
        ),
        EndpointCase(
            name="admin-queue-policy",
            method="PUT",
            path=lambda index: f"/admin/concerts/{uuid_id('concert', 'bench', index % policy_count)}/queue-policy",
            status=200,
            json_body=lambda _: {
                "enabled": True,
                "maxEntrantsPerMinute": 100,
                "waitingRoomUrl": "https://queue.example.com",
            },
        ),
        EndpointCase(
            name="admin-traffic-policy",
            method="PUT",
            path=lambda index: f"/admin/concerts/{uuid_id('concert', 'bench', index % policy_count)}/traffic-policy",
            status=200,
            json_body=lambda _: {
                "macroProtectionEnabled": True,
                "maxRequestsPerUserPerMinute": 30,
                "blockSuspiciousTraffic": True,
            },
        ),
    ]


def _measure_endpoint(client: TestClient, endpoint: EndpointCase, config: BenchmarkConfig) -> dict[str, Any]:
    durations: list[float] = []
    total = config.warmup + config.samples
    for iteration in range(total):
        started = time.perf_counter_ns()
        response = client.request(
            endpoint.method,
            endpoint.path(iteration),
            json=endpoint.json_body(iteration) if endpoint.json_body else None,
            headers=endpoint.headers(iteration) if endpoint.headers else None,
        )
        elapsed_ms = (time.perf_counter_ns() - started) / 1_000_000
        if response.status_code != endpoint.status:
            pytest.fail(f"{endpoint.name} returned HTTP {response.status_code}: {response.text}")
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


def _query_analysis(session: Session, targets: SeedTargets, config: BenchmarkConfig) -> list[dict[str, Any]]:
    sample_note = percentile_interpretation(config.samples)
    tables = config.preset.service_tables(SERVICE_NAME)
    heavy_rows = int(tables["reservations"] * config.preset.user_distribution["heavy"])
    return [
        {
            "endpoint": "create-reservation",
            "queries": [
                explain_postgres_sql(
                    session,
                    label="active-reservation-conflict-check",
                    sql=(
                        "SELECT id FROM reservations "
                        "WHERE performance_id = :performance_id AND seat_id = :seat_id AND status IN ('pending', 'paid')"
                    ),
                    params={
                        "performance_id": uuid_id("performance", "create-explain"),
                        "seat_id": uuid_id("seat", "create-explain"),
                    },
                    query_shape="SELECT active reservation WHERE performance_id, seat_id, status",
                    index_decision="중복 방어는 active_seat_key unique를 유지한다. active lookup은 복합/부분 index 후보로 남긴다.",
                    data_analysis=f"reservations={tables['reservations']:,}. 생성 path는 conflict check + insert + commit 비용이다.",
                )
            ],
            "sampleInterpretation": sample_note,
        },
        {
            "endpoint": "list-my-reservations-normal-first-page",
            "queries": [
                explain_postgres_sql(
                    session,
                    label="reservations-by-normal-user",
                    sql="SELECT id, created_at FROM reservations WHERE user_id = :user_id ORDER BY created_at DESC LIMIT 20",
                    params={"user_id": targets.list_normal_user_id},
                    query_shape="SELECT reservations WHERE user_id ORDER BY created_at DESC LIMIT 20",
                    index_decision="목록 tail이 커지면 (user_id, created_at desc) 복합 index를 검토한다.",
                    data_analysis="일반 사용자는 seed에서 samples 수준만 보장되어 scan 폭이 작다.",
                )
            ],
            "sampleInterpretation": sample_note,
        },
        {
            "endpoint": "list-my-reservations-heavy-first-page",
            "queries": [
                explain_postgres_sql(
                    session,
                    label="reservations-by-heavy-user",
                    sql="SELECT id, created_at FROM reservations WHERE user_id = :user_id ORDER BY created_at DESC LIMIT 20",
                    params={"user_id": targets.list_heavy_user_id},
                    query_shape="SELECT reservations WHERE user_id ORDER BY created_at DESC LIMIT 20",
                    index_decision="헤비 사용자 p95가 커지면 (user_id, created_at desc)로 정렬 비용을 줄인다.",
                    data_analysis=f"heavy 비율 {config.preset.user_distribution['heavy']:.0%}라 한 사용자에게 약 {heavy_rows:,}건이 몰릴 수 있다.",
                )
            ],
            "sampleInterpretation": sample_note,
        },
        {
            "endpoint": "get/cancel/expire-reservation",
            "queries": [
                explain_postgres_sql(
                    session,
                    label="reservation-by-id",
                    sql="SELECT id, status FROM reservations WHERE id = :reservation_id",
                    params={"reservation_id": targets.get_reservation_id},
                    query_shape="SELECT/UPDATE reservations WHERE id",
                    index_decision="단건 상태 변경은 PK 유지.",
                    data_analysis="p95 outlier는 row scan보다 transaction/commit wall time 후보가 크다.",
                )
            ],
            "sampleInterpretation": sample_note,
        },
        {
            "endpoint": "sales/policy endpoints",
            "queries": [
                explain_postgres_sql(
                    session,
                    label="reservation-counts-for-concert",
                    sql="SELECT status, count(*) FROM reservations WHERE concert_id = :concert_id GROUP BY status",
                    params={"concert_id": targets.concert_id},
                    query_shape="SELECT reservation counts WHERE concert_id GROUP BY status",
                    index_decision="판매 집계가 커지면 (concert_id, status) 복합 index를 검토한다.",
                    data_analysis=f"concerts={config.preset.catalog['concerts']:,}, reservations={tables['reservations']:,}. 상태별 count 비용을 같이 본다.",
                )
            ],
            "sampleInterpretation": sample_note,
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
            "FastAPI TestClient measures router, dependency, service, and repository code paths together.",
            "PostgreSQL runs in testcontainers postgres:16-alpine and is removed when the test ends.",
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
