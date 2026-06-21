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
from server.ids import deterministic_uuid_string
from sqlalchemy import create_engine, insert, text
from sqlalchemy.orm import Session, sessionmaker

SERVICE_REPO_ROOT = Path(__file__).resolve().parents[4]
sys.path.insert(0, str(SERVICE_REPO_ROOT))

from tests.benchmarks.api_presets import ApiBenchmarkPreset, chunked, load_preset, user_id_for
from tests.benchmarks.query_analysis import explain_postgres_sql, percentile_interpretation

from app.database import Base, get_db
from app.kafka import get_kafka_producer
from app.models import ProcessedEvent, Ticket
from app.routers.tickets import router as ticket_router


SERVICE_NAME = "ticket-service"


@dataclass(frozen=True)
class BenchmarkConfig:
    samples: int
    warmup: int
    artifact_dir: Path
    preset: ApiBenchmarkPreset


@dataclass(frozen=True)
class SeedTargets:
    ticket_id: str
    normal_user_id: str
    heavy_user_id: str
    cursor: str


@dataclass(frozen=True)
class EndpointCase:
    name: str
    method: str
    path: Callable[[int], str]
    status: int
    json_body: Callable[[int], dict[str, Any]] | None = None
    headers: Callable[[int], dict[str, str]] | None = None


def test_ticket_api_benchmark_outputs_artifact(request: pytest.FixtureRequest) -> None:
    if not request.config.getoption("--run-ticket-api-benchmark"):
        pytest.skip("use --run-ticket-api-benchmark to run the isolated PostgreSQL benchmark")

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
                    for endpoint in _benchmark_endpoints(targets)
                ]
            with factory() as session:
                query_analysis = _query_analysis(session, targets, config)
            artifact = _artifact(started_at, config, metrics, query_analysis)
            _write_artifact(config.artifact_dir, artifact, started_at)
        finally:
            engine.dispose()


def _benchmark_config(request: pytest.FixtureRequest) -> BenchmarkConfig:
    try:
        preset = load_preset(request.config.getoption("--ticket-benchmark-preset"))
    except ValueError as exc:
        pytest.fail(str(exc))
    config = BenchmarkConfig(
        samples=request.config.getoption("--ticket-benchmark-samples"),
        warmup=request.config.getoption("--ticket-benchmark-warmup"),
        artifact_dir=Path(request.config.getoption("--ticket-benchmark-artifact-dir")),
        preset=preset,
    )
    if config.samples < 1:
        pytest.fail("ticket benchmark samples must be a positive integer")
    if config.warmup < 0:
        pytest.fail("ticket benchmark warmup must be zero or greater")
    return config


def _benchmark_app(factory: sessionmaker[Session]) -> FastAPI:
    app = FastAPI(title=f"{SERVICE_NAME}-api-benchmark")
    app.include_router(ticket_router)
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
    tables = config.preset.service_tables(SERVICE_NAME)
    now = datetime(2026, 7, 1, 12, 0, tzinfo=UTC)
    normal_user_id = user_id_for(SERVICE_NAME, "normal")
    heavy_user_id = user_id_for(SERVICE_NAME, "heavy")
    for rows in chunked(_ticket_rows(config, now, normal_user_id, heavy_user_id)):
        session.execute(insert(Ticket), rows)
    for rows in chunked(_processed_event_rows(tables["processed_events"], now)):
        session.execute(insert(ProcessedEvent), rows)
    return SeedTargets(
        ticket_id=_benchmark_uuid("ticket", 0),
        normal_user_id=normal_user_id,
        heavy_user_id=heavy_user_id,
        cursor=_benchmark_uuid("ticket", 19),
    )


def _ticket_rows(
    config: BenchmarkConfig,
    now: datetime,
    normal_user_id: str,
    heavy_user_id: str,
) -> Iterator[dict[str, Any]]:
    total = config.preset.service_tables(SERVICE_NAME)["tickets"]
    heavy_rows = min(max(40, int(total * config.preset.user_distribution["heavy"])), total)
    normal_rows = min(max(25, config.samples), max(total - heavy_rows, 0))
    for index in range(total):
        if index < heavy_rows:
            user_id = heavy_user_id
        elif index < heavy_rows + normal_rows:
            user_id = normal_user_id
        else:
            user_id = _ticket_user_id(index, total, config)
        yield {
            "id": _benchmark_uuid("ticket", index),
            "reservation_id": _benchmark_uuid("reservation", index),
            "user_id": user_id,
            "concert_id": _benchmark_uuid("concert", index % config.preset.catalog["concerts"]),
            "seat_id": _benchmark_uuid("seat", index),
            "status": "ISSUED",
            "qr_url": None,
            "pdf_url": None,
            "issued_at": now - timedelta(seconds=index),
        }


def _processed_event_rows(total: int, now: datetime) -> Iterator[dict[str, Any]]:
    for index in range(total):
        yield {
            "event_id": _benchmark_uuid("payment-approved-event", index),
            "ticket_id": _benchmark_uuid("ticket", index),
            "created_at": now - timedelta(seconds=index),
        }


def _ticket_user_id(index: int, total: int, config: BenchmarkConfig) -> str:
    distribution = config.preset.user_distribution
    heavy_cutoff = int(total * distribution["heavy"])
    repeat_cutoff = heavy_cutoff + int(total * distribution["repeat"])
    if index < heavy_cutoff:
        return user_id_for(SERVICE_NAME, "heavy")
    if index < repeat_cutoff:
        return user_id_for(SERVICE_NAME, "repeat", index % max(1, int(total * distribution["repeat"] / 8)))
    return user_id_for(SERVICE_NAME, "normal", index % max(1, min(config.preset.active_users, total)))


def _benchmark_endpoints(targets: SeedTargets) -> list[EndpointCase]:
    return [
        EndpointCase(
            name="issue-ticket",
            method="POST",
            path=lambda _: "/tickets/issue",
            status=200,
            json_body=lambda index: {
                "reservationId": _benchmark_uuid("reservation-issue", index),
                "userId": targets.normal_user_id,
                "concertId": _benchmark_uuid("concert-issue", index),
                "seatId": _benchmark_uuid("seat-issue", index),
            },
        ),
        EndpointCase(
            name="list-my-tickets-normal-first-page",
            method="GET",
            path=lambda _: "/tickets/me?limit=20",
            status=200,
            headers=lambda _: _user_headers(targets.normal_user_id),
        ),
        EndpointCase(
            name="list-my-tickets-heavy-first-page",
            method="GET",
            path=lambda _: "/tickets/me?limit=20",
            status=200,
            headers=lambda _: _user_headers(targets.heavy_user_id),
        ),
        EndpointCase(
            name="list-my-tickets-heavy-cursor-next-page",
            method="GET",
            path=lambda _: f"/tickets/me?limit=20&cursor={targets.cursor}",
            status=200,
            headers=lambda _: _user_headers(targets.heavy_user_id),
        ),
        EndpointCase(
            name="get-ticket",
            method="GET",
            path=lambda _: f"/tickets/{targets.ticket_id}",
            status=200,
            headers=lambda _: _user_headers(targets.heavy_user_id),
        ),
    ]


def _user_headers(user_id: str) -> dict[str, str]:
    return {"X-User-Id": user_id, "X-User-Role": "USER"}


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
    heavy_rows = int(tables["tickets"] * config.preset.user_distribution["heavy"])
    return [
        {
            "endpoint": "issue-ticket",
            "queries": [
                explain_postgres_sql(
                    session,
                    label="ticket-by-reservation-id",
                    sql="SELECT id FROM tickets WHERE reservation_id = :reservation_id LIMIT 1",
                    params={"reservation_id": _benchmark_uuid("reservation-issue", "explain")},
                    query_shape="SELECT tickets WHERE reservation_id, INSERT ticket",
                    index_decision="중복 발급 방어에는 reservation_id unique index 유지.",
                    data_analysis=f"tickets={tables['tickets']:,}. S3/Kafka는 제외되어 DB insert와 local artifact path 중심이다.",
                )
            ],
            "sampleInterpretation": sample_note,
        },
        {
            "endpoint": "list-my-tickets-normal-first-page",
            "queries": [
                explain_postgres_sql(
                    session,
                    label="tickets-by-normal-user",
                    sql="SELECT id, issued_at FROM tickets WHERE user_id = :user_id ORDER BY id LIMIT 21",
                    params={"user_id": targets.normal_user_id},
                    query_shape="SELECT tickets WHERE user_id ORDER BY id LIMIT 21",
                    index_decision="현재 수치는 낮지만 목록 tail이 커지면 (user_id, id) 복합 index를 검토한다.",
                    data_analysis="일반 사용자는 보장 row가 작아 응답/직렬화 비용이 작다.",
                )
            ],
            "sampleInterpretation": sample_note,
        },
        {
            "endpoint": "list-my-tickets-heavy-first/cursor",
            "queries": [
                explain_postgres_sql(
                    session,
                    label="tickets-by-heavy-user-first-page",
                    sql="SELECT id, issued_at FROM tickets WHERE user_id = :user_id ORDER BY id LIMIT 21",
                    params={"user_id": targets.heavy_user_id},
                    query_shape="SELECT tickets WHERE user_id ORDER BY id LIMIT 21",
                    index_decision="heavy tail이 커지면 (user_id, id) 복합 index가 자연스러운 다음 후보다.",
                    data_analysis=f"heavy 비율 {config.preset.user_distribution['heavy']:.0%}라 약 {heavy_rows:,}건이 한 사용자에게 몰릴 수 있다.",
                ),
                explain_postgres_sql(
                    session,
                    label="tickets-by-heavy-user-cursor",
                    sql="SELECT id, issued_at FROM tickets WHERE user_id = :user_id AND id > :cursor ORDER BY id LIMIT 21",
                    params={"user_id": targets.heavy_user_id, "cursor": targets.cursor},
                    query_shape="SELECT tickets WHERE user_id AND id > cursor ORDER BY id LIMIT 21",
                    index_decision="cursor pagination은 유지. 복합 index 결정은 first/cursor page를 함께 보고 판단한다.",
                    data_analysis="cursor 조건이 있어도 heavy 사용자 row 분포가 tail 비용을 좌우한다.",
                ),
            ],
            "sampleInterpretation": sample_note,
        },
        {
            "endpoint": "get-ticket",
            "queries": [
                explain_postgres_sql(
                    session,
                    label="ticket-by-id",
                    sql="SELECT id, user_id FROM tickets WHERE id = :ticket_id",
                    params={"ticket_id": targets.ticket_id},
                    query_shape="SELECT tickets WHERE id",
                    index_decision="단건 조회는 PK 유지.",
                    data_analysis="전체 tickets 규모보다 권한 확인과 응답 변환 비용이 크다.",
                )
            ],
            "sampleInterpretation": sample_note,
        },
    ]


def _percentile(values: list[float], percentile: int) -> float:
    ordered = sorted(values)
    index = max(math.ceil(len(ordered) * percentile / 100) - 1, 0)
    return ordered[index]


def _benchmark_uuid(*parts: object) -> str:
    return deterministic_uuid_string(SERVICE_NAME, *parts)


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
            "S3 and Kafka are left unconfigured so issue-ticket measures local API and DB work only.",
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
