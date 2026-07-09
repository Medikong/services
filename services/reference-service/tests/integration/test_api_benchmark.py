from __future__ import annotations

from collections.abc import Callable
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

from tests.benchmarks.api_presets import ApiBenchmarkPreset, chunked, load_preset
from tests.benchmarks.query_analysis import explain_postgres_sql, percentile_interpretation

from app import entities as model
from app.database import Base
from app.dependencies import get_db
from app.exceptions import register_exception_handlers
from app.routers import router as concert_router


SERVICE_NAME = "concert-service"


def uuid_id(*parts: object) -> str:
    return deterministic_uuid_string(SERVICE_NAME, *parts)


@dataclass(frozen=True)
class BenchmarkConfig:
    samples: int
    warmup: int
    artifact_dir: Path
    preset: ApiBenchmarkPreset

    @property
    def tables(self) -> dict[str, int]:
        return self.preset.service_tables(SERVICE_NAME)

    @property
    def concerts(self) -> int:
        return self.tables["concerts"]

    @property
    def showtimes(self) -> int:
        return self.tables["showtimes"]

    @property
    def seats(self) -> int:
        return self.tables["seats"]

    @property
    def seats_per_showtime(self) -> int:
        return self.seats // self.showtimes

    @property
    def showtimes_per_concert(self) -> int:
        return self.showtimes // self.concerts


@dataclass(frozen=True)
class SeedTargets:
    concert_id: str
    performance_id: str
    date: str
    year_month: str


@dataclass(frozen=True)
class EndpointCase:
    name: str
    method: str
    path: Callable[[int], str]
    status: int


def test_concert_api_benchmark_outputs_artifact(request: pytest.FixtureRequest) -> None:
    if not request.config.getoption("--run-concert-api-benchmark"):
        pytest.skip("use --run-concert-api-benchmark to run the isolated PostgreSQL benchmark")

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
        preset = load_preset(request.config.getoption("--concert-benchmark-preset"))
    except ValueError as exc:
        pytest.fail(str(exc))
    config = BenchmarkConfig(
        samples=request.config.getoption("--concert-benchmark-samples"),
        warmup=request.config.getoption("--concert-benchmark-warmup"),
        artifact_dir=Path(request.config.getoption("--concert-benchmark-artifact-dir")),
        preset=preset,
    )
    if config.samples < 1:
        pytest.fail("concert benchmark samples must be a positive integer")
    if config.warmup < 0:
        pytest.fail("concert benchmark warmup must be zero or greater")
    if config.concerts < 1 or config.showtimes < 1 or config.seats < 1:
        pytest.fail("concert benchmark preset must include positive concerts, showtimes, and seats")
    if config.showtimes % config.concerts != 0:
        pytest.fail("concert benchmark showtimes must divide evenly by concerts")
    if config.seats % config.showtimes != 0:
        pytest.fail("concert benchmark seats must divide evenly by showtimes")
    if config.tables["seat_grades"] != config.showtimes * 4:
        pytest.fail("concert benchmark seat_grades must equal showtimes * 4")
    return config


def _benchmark_app(factory: sessionmaker[Session]) -> FastAPI:
    app = FastAPI(title=f"{SERVICE_NAME}-api-benchmark")
    register_exception_handlers(app)
    app.include_router(concert_router)

    def override_get_db():
        db = factory()
        try:
            yield db
        finally:
            db.close()

    app.dependency_overrides[get_db] = override_get_db
    return app


def _seed_dataset(session: Session, config: BenchmarkConfig) -> SeedTargets:
    base = datetime(2026, 7, 1, 12, 0, tzinfo=UTC)
    session.execute(insert(model.Venue), list(_venue_rows(config)))
    session.execute(insert(model.Concert), list(_concert_rows(config, base)))
    session.execute(insert(model.Showtime), list(_showtime_rows(config, base)))
    session.execute(insert(model.SeatGrade), list(_seat_grade_rows(config)))
    for rows in chunked(_seat_rows(config)):
        session.execute(insert(model.Seat), rows)

    target_index = config.concerts - 1
    target_showtime_id = uuid_id("showtime", target_index, 0)
    target_date = (base + timedelta(hours=target_index % 8)).date()
    return SeedTargets(
        concert_id=uuid_id("concert", target_index),
        performance_id=target_showtime_id,
        date=target_date.isoformat(),
        year_month=f"{target_date:%Y-%m}",
    )


def _venue_rows(config: BenchmarkConfig):
    for concert_index in range(config.concerts):
        yield {
            "id": uuid_id("venue", concert_index),
            "name": f"Benchmark Hall {concert_index:04d}",
            "address": "Seoul",
            "total_seats": config.seats_per_showtime,
        }


def _concert_rows(config: BenchmarkConfig, base: datetime):
    for concert_index in range(config.concerts):
        yield {
            "id": uuid_id("concert", concert_index),
            "provider_id": f"provider-bench-{concert_index % 10:02d}",
            "title": f"Benchmark Concert {concert_index:04d}",
            "description": "Deterministic public API benchmark fixture",
            "poster_url": f"https://example.test/posters/{concert_index:04d}.jpg",
            "age_rating": "ALL",
            "running_minutes": 120,
            "status": "open",
            "created_at": base + timedelta(minutes=concert_index),
            "updated_at": None,
            "opens_at": base,
            "open_schedule_status": "opened",
            "last_reviewed_at": base,
            "review_reason": None,
        }


def _showtime_rows(config: BenchmarkConfig, base: datetime):
    for concert_index in range(config.concerts):
        for showtime_index in range(config.showtimes_per_concert):
            starts_at = base + timedelta(days=showtime_index, hours=concert_index % 8)
            yield {
                "id": uuid_id("showtime", concert_index, showtime_index),
                "concert_id": uuid_id("concert", concert_index),
                "venue_id": uuid_id("venue", concert_index),
                "starts_at": starts_at,
                "ends_at": starts_at + timedelta(hours=2),
                "status": "open",
            }


def _seat_grade_rows(config: BenchmarkConfig):
    for concert_index in range(config.concerts):
        for showtime_index in range(config.showtimes_per_concert):
            showtime_id = uuid_id("showtime", concert_index, showtime_index)
            for grade_index, section in enumerate(("A", "B", "C", "D")):
                yield {
                    "id": uuid_id("grade", concert_index, showtime_index, section),
                    "showtime_id": showtime_id,
                    "name": section,
                    "price": 50000 + grade_index * 25000,
                    "color": None,
                }


def _seat_rows(config: BenchmarkConfig):
    for concert_index in range(config.concerts):
        for showtime_index in range(config.showtimes_per_concert):
            showtime_id = uuid_id("showtime", concert_index, showtime_index)
            for seat_index in range(config.seats_per_showtime):
                section = ("A", "B", "C", "D")[seat_index % 4]
                row = f"{seat_index // 20 + 1:02d}"
                number = f"{seat_index % 20 + 1:02d}"
                status = "sellable"
                if seat_index % 97 == 0:
                    status = "blocked"
                elif seat_index % 89 == 0:
                    status = "reserved"
                yield {
                    "id": uuid_id("seat", concert_index, showtime_index, section, row, number),
                    "showtime_id": showtime_id,
                    "section": section,
                    "row_label": row,
                    "number": number,
                    "status": status,
                }


def _benchmark_endpoints(targets: SeedTargets) -> list[EndpointCase]:
    return [
        EndpointCase(
            name="recommended-concerts",
            method="GET",
            path=lambda _: "/concerts/recommended?sort=latest&limit=10",
            status=200,
        ),
        EndpointCase(
            name="concert-detail",
            method="GET",
            path=lambda _: f"/concerts/{targets.concert_id}",
            status=200,
        ),
        EndpointCase(
            name="concert-calendar",
            method="GET",
            path=lambda _: f"/concerts/{targets.concert_id}/calendar?yearMonth={targets.year_month}",
            status=200,
        ),
        EndpointCase(
            name="date-performances",
            method="GET",
            path=lambda _: f"/concerts/{targets.concert_id}/dates/{targets.date}/performances",
            status=200,
        ),
        EndpointCase(
            name="seat-map",
            method="GET",
            path=lambda _: f"/performances/{targets.performance_id}/seat-map",
            status=200,
        ),
    ]


def _measure_endpoint(client: TestClient, endpoint: EndpointCase, config: BenchmarkConfig) -> dict[str, Any]:
    durations: list[float] = []
    total = config.warmup + config.samples
    for iteration in range(total):
        started = time.perf_counter_ns()
        response = client.request(endpoint.method, endpoint.path(iteration))
        elapsed_ms = (time.perf_counter_ns() - started) / 1_000_000
        if response.status_code != endpoint.status:
            pytest.fail(f"{endpoint.name} returned HTTP {response.status_code}: {response.text}")
        _assert_endpoint_shape(endpoint.name, response.json())
        if iteration >= config.warmup:
            durations.append(elapsed_ms)
    return _metric(endpoint, durations, config)


def _assert_endpoint_shape(name: str, body: dict[str, Any]) -> None:
    validators: dict[str, Callable[[dict[str, Any]], bool]] = {
        "recommended-concerts": lambda payload: len(payload["items"]) == 10 and payload["page"]["limit"] == 10,
        "concert-detail": lambda payload: payload["concertId"] and "performances" not in payload,
        "concert-calendar": lambda payload: len(payload["days"]) >= 28 and any(day["bookable"] for day in payload["days"]),
        "date-performances": lambda payload: len(payload["performances"]) >= 1,
        "seat-map": lambda payload: len(payload["sections"]) == 4 and len(payload["seats"]) >= 1,
    }
    if not validators[name](body):
        pytest.fail(f"{name} returned an unexpected response shape: {body}")


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
    day_start = datetime.fromisoformat(targets.date).replace(tzinfo=UTC)
    day_end = day_start + timedelta(days=1)
    month_start = datetime.fromisoformat(f"{targets.year_month}-01").replace(tzinfo=UTC)
    month_end = month_start + timedelta(days=32)
    month_end = month_end.replace(day=1)
    return [
        {
            "endpoint": "recommended-concerts",
            "queries": [
                explain_postgres_sql(
                    session,
                    label="recommended-concerts",
                    sql="SELECT id, created_at FROM concerts ORDER BY created_at DESC, id DESC LIMIT 11",
                    params={},
                    query_shape="SELECT concerts ORDER BY created_at DESC, id DESC LIMIT 11",
                    index_decision="추천 first/cursor page는 (created_at, id) index 유지.",
                    data_analysis=f"concerts={tables['concerts']:,}. showtimes selectinload가 붙어 목록 카드 응답 비용도 포함된다.",
                )
            ],
            "sampleInterpretation": sample_note,
        },
        {
            "endpoint": "concert-detail",
            "queries": [
                explain_postgres_sql(
                    session,
                    label="concert-detail-base",
                    sql="SELECT id, title FROM concerts WHERE id = :concert_id",
                    params={"concert_id": targets.concert_id},
                    query_shape="SELECT concert by id",
                    index_decision="상세 기본 row는 PK index로 충분하다.",
                    data_analysis="공연 상세는 단일 row 조회 뒤 showtime/grade selectinload 비용이 붙는다.",
                ),
                explain_postgres_sql(
                    session,
                    label="concert-detail-showtimes",
                    sql="SELECT id, starts_at FROM showtimes WHERE concert_id = :concert_id ORDER BY starts_at",
                    params={"concert_id": targets.concert_id},
                    query_shape="SELECT showtimes WHERE concert_id ORDER BY starts_at",
                    index_decision="showtimes(concert_id, starts_at) index 유지.",
                    data_analysis="공연당 회차는 약 3건이라 scan 폭은 작다.",
                ),
            ],
            "sampleInterpretation": sample_note,
        },
        {
            "endpoint": "concert-calendar",
            "queries": [
                explain_postgres_sql(
                    session,
                    label="calendar-bookable-exists",
                    sql=(
                        "SELECT starts_at FROM showtimes "
                        "WHERE concert_id = :concert_id AND starts_at >= :start_at AND starts_at < :end_at "
                        "AND status NOT IN ('closed', 'canceled', 'sold_out') "
                        "AND EXISTS (SELECT 1 FROM seats WHERE seats.showtime_id = showtimes.id AND seats.status = 'sellable') "
                        "ORDER BY starts_at"
                    ),
                    params={"concert_id": targets.concert_id, "start_at": month_start, "end_at": month_end},
                    query_shape="SELECT showtimes range + EXISTS seats",
                    index_decision="좌석 row 전체 로딩 대신 EXISTS 유지. 추가 인덱스보다 응답 생성 outlier를 본다.",
                    data_analysis=f"seats={tables['seats']:,}지만 showtime당 sellable 존재만 확인한다.",
                )
            ],
            "sampleInterpretation": sample_note,
        },
        {
            "endpoint": "date-performances",
            "queries": [
                explain_postgres_sql(
                    session,
                    label="date-performances",
                    sql=(
                        "SELECT id, starts_at FROM showtimes "
                        "WHERE concert_id = :concert_id AND starts_at >= :start_at AND starts_at < :end_at "
                        "ORDER BY starts_at"
                    ),
                    params={"concert_id": targets.concert_id, "start_at": day_start, "end_at": day_end},
                    query_shape="SELECT showtimes WHERE concert_id AND starts_at range",
                    index_decision="날짜별 회차는 현재 복합 인덱스 유지.",
                    data_analysis="공연당 회차 수가 작아 DB plan보다 TestClient/SQLAlchemy wall time 변동을 같이 본다.",
                )
            ],
            "sampleInterpretation": sample_note,
        },
        {
            "endpoint": "seat-map",
            "queries": [
                explain_postgres_sql(
                    session,
                    label="seat-map-seats",
                    sql="SELECT id, section, row_label, number FROM seats WHERE showtime_id = :showtime_id ORDER BY section, row_label, number",
                    params={"showtime_id": targets.performance_id},
                    query_shape="SELECT seats WHERE showtime_id ORDER BY section,row,number",
                    index_decision="좌석도는 showtime_id index 유지. 대형 공연장은 section pagination을 별도 결정한다.",
                    data_analysis=f"좌석 응답 크기가 직접 비용이 된다. 현재 preset은 회차당 약 {config.seats_per_showtime:,}석이다.",
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
