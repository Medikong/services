from __future__ import annotations

from collections.abc import Callable, Iterator
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
import json
import math
import os
from pathlib import Path
import subprocess
import time
from typing import Any

from fastapi import FastAPI
from fastapi.testclient import TestClient
import pytest
from sqlalchemy import Select, create_engine, event, insert, select, text
from sqlalchemy.orm import Session, sessionmaker
from server.ids import deterministic_uuid_string

from app import entities as model
from app.database import Base
from app.dependencies import get_db
from app.exceptions import register_exception_handlers
from app.routers import router as concert_router


SERVICE_NAME = "concert-service"


def uuid_id(*parts: object) -> str:
    return deterministic_uuid_string("concert-service-public", *parts)


@dataclass(frozen=True)
class BenchmarkConfig:
    concerts: int
    showtimes_per_concert: int
    seats_per_showtime: int
    samples: int
    warmup: int
    artifact_dir: Path

    @property
    def performances(self) -> int:
        return self.concerts * self.showtimes_per_concert

    @property
    def seats(self) -> int:
        return self.performances * self.seats_per_showtime

    @property
    def grades(self) -> int:
        return self.performances * 4


@dataclass(frozen=True)
class SeedTargets:
    concert_id: str
    performance_id: str
    date: str
    year_month: str
    cursor_created_at: datetime


class SqlRecorder:
    def __init__(self) -> None:
        self.current_endpoint: str | None = None
        self.samples_by_endpoint: dict[str, list[dict[str, Any]]] = {}

    def start_endpoint(self, endpoint: str) -> None:
        self.current_endpoint = endpoint
        self.samples_by_endpoint.setdefault(endpoint, []).append({"count": 0, "durationMs": 0.0, "queries": []})

    def stop_endpoint(self) -> None:
        self.current_endpoint = None

    def record(self, duration_ms: float, statement: str) -> None:
        if self.current_endpoint is None:
            return
        sample = self.samples_by_endpoint[self.current_endpoint][-1]
        sample["count"] += 1
        sample["durationMs"] += duration_ms
        sample["queries"].append({"durationMs": round(duration_ms, 3), "statement": _summarize_sql(statement)})


def test_public_api_large_dataset_benchmark(request: pytest.FixtureRequest) -> None:
    if not request.config.getoption("--run-public-api-benchmark"):
        pytest.skip("use --run-public-api-benchmark to run the isolated PostgreSQL benchmark")

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
        sql_recorder = _install_sql_recorder(engine)
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
                endpoints = _benchmark_endpoints(targets)
                metrics = [_measure_endpoint(client, endpoint, config, sql_recorder) for endpoint in endpoints]
            with factory() as session:
                query_plans = _collect_query_plans(session, targets)
            artifact = {
                "generatedAt": started_at.isoformat(),
                "finishedAt": datetime.now(UTC).isoformat(),
                "service": _git_info(Path.cwd().parents[1]),
                "seed": {
                    "concerts": config.concerts,
                    "performances": config.performances,
                    "seats": config.seats,
                    "grades": config.grades,
                    "showtimesPerConcert": config.showtimes_per_concert,
                    "seatsPerShowtime": config.seats_per_showtime,
                },
                "benchmark": {
                    "warmup": config.warmup,
                    "samplesPerEndpoint": config.samples,
                    "endpoints": metrics,
                },
                "queryPlans": query_plans,
                "constraints": [
                    "FastAPI TestClient를 사용해 HTTP router, dependency, service, repository path를 함께 측정했다.",
                    "PostgreSQL은 testcontainers postgres:16-alpine으로 생성했고 benchmark 종료 시 컨테이너가 정리된다.",
                ],
            }
            _write_artifact(config.artifact_dir, artifact, started_at)
        finally:
            engine.dispose()


def _benchmark_config(request: pytest.FixtureRequest) -> BenchmarkConfig:
    config = BenchmarkConfig(
        concerts=request.config.getoption("--public-benchmark-concerts"),
        showtimes_per_concert=request.config.getoption("--public-benchmark-showtimes-per-concert"),
        seats_per_showtime=request.config.getoption("--public-benchmark-seats-per-showtime"),
        samples=request.config.getoption("--public-benchmark-samples"),
        warmup=request.config.getoption("--public-benchmark-warmup"),
        artifact_dir=Path(request.config.getoption("--public-benchmark-artifact-dir")),
    )
    if min(config.concerts, config.showtimes_per_concert, config.seats_per_showtime, config.samples) < 1:
        pytest.fail("public API benchmark sizes and samples must be positive integers")
    if config.warmup < 0:
        pytest.fail("public API benchmark warmup must be zero or greater")
    return config


def _benchmark_app(factory: sessionmaker[Session]) -> FastAPI:
    app = FastAPI(title="concert-service-public-api-benchmark")
    register_exception_handlers(app)
    app.include_router(concert_router)

    def override_get_db() -> Iterator[Session]:
        db = factory()
        try:
            yield db
        finally:
            db.close()

    app.dependency_overrides[get_db] = override_get_db
    return app


def _seed_dataset(session: Session, config: BenchmarkConfig) -> SeedTargets:
    base = datetime(2026, 7, 1, 12, 0, tzinfo=UTC)
    venues: list[dict[str, Any]] = []
    concerts: list[dict[str, Any]] = []
    showtimes: list[dict[str, Any]] = []
    grades: list[dict[str, Any]] = []
    seats: list[dict[str, Any]] = []

    for concert_index in range(config.concerts):
        concert_id = uuid_id("concert", concert_index)
        venue_id = uuid_id("venue", concert_index)
        venues.append(
            {
                "id": venue_id,
                "name": f"Benchmark Hall {concert_index:04d}",
                "address": "Seoul",
                "total_seats": config.seats_per_showtime,
            }
        )
        concerts.append(
            {
                "id": concert_id,
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
        )
        for showtime_index in range(config.showtimes_per_concert):
            showtime_id = uuid_id("showtime", concert_index, showtime_index)
            starts_at = base + timedelta(days=showtime_index, hours=concert_index % 8)
            showtimes.append(
                {
                    "id": showtime_id,
                    "concert_id": concert_id,
                    "venue_id": venue_id,
                    "starts_at": starts_at,
                    "ends_at": starts_at + timedelta(hours=2),
                    "status": "open",
                }
            )
            for grade_index, section in enumerate(("A", "B", "C", "D")):
                grades.append(
                    {
                        "id": uuid_id("grade", concert_index, showtime_index, section),
                        "showtime_id": showtime_id,
                        "name": section,
                        "price": 50000 + grade_index * 25000,
                        "color": None,
                    }
                )
            for seat_index in range(config.seats_per_showtime):
                section = ("A", "B", "C", "D")[seat_index % 4]
                row = f"{seat_index // 20 + 1:02d}"
                number = f"{seat_index % 20 + 1:02d}"
                status = "sellable"
                if seat_index % 97 == 0:
                    status = "blocked"
                elif seat_index % 89 == 0:
                    status = "reserved"
                seats.append(
                    {
                        "id": uuid_id("seat", concert_index, showtime_index, section, row, number),
                        "showtime_id": showtime_id,
                        "section": section,
                        "row_label": row,
                        "number": number,
                        "status": status,
                    }
                )

    session.execute(insert(model.Venue), venues)
    session.execute(insert(model.Concert), concerts)
    session.execute(insert(model.Showtime), showtimes)
    session.execute(insert(model.SeatGrade), grades)
    for start in range(0, len(seats), 5000):
        session.execute(insert(model.Seat), seats[start : start + 5000])

    target_index = config.concerts - 1
    target_showtime_id = uuid_id("showtime", target_index, 0)
    target_date = (base + timedelta(hours=target_index % 8)).date()
    cursor_index = max(config.concerts // 2, 1)
    return SeedTargets(
        concert_id=uuid_id("concert", target_index),
        performance_id=target_showtime_id,
        date=target_date.isoformat(),
        year_month=f"{target_date:%Y-%m}",
        cursor_created_at=base + timedelta(minutes=cursor_index),
    )


def _benchmark_endpoints(targets: SeedTargets) -> list[dict[str, str]]:
    return [
        {
            "name": "recommended-concerts",
            "method": "GET",
            "path": "/concerts/recommended?sort=latest&limit=10",
        },
        {
            "name": "concert-detail",
            "method": "GET",
            "path": f"/concerts/{targets.concert_id}",
        },
        {
            "name": "concert-calendar",
            "method": "GET",
            "path": f"/concerts/{targets.concert_id}/calendar?yearMonth={targets.year_month}",
        },
        {
            "name": "date-performances",
            "method": "GET",
            "path": f"/concerts/{targets.concert_id}/dates/{targets.date}/performances",
        },
        {
            "name": "seat-map",
            "method": "GET",
            "path": f"/performances/{targets.performance_id}/seat-map",
        },
    ]


def _install_sql_recorder(engine) -> SqlRecorder:
    recorder = SqlRecorder()

    @event.listens_for(engine, "before_cursor_execute")
    def before_cursor_execute(conn, cursor, statement, parameters, context, executemany):
        context._benchmark_query_started_ns = time.perf_counter_ns()

    @event.listens_for(engine, "after_cursor_execute")
    def after_cursor_execute(conn, cursor, statement, parameters, context, executemany):
        started_ns = getattr(context, "_benchmark_query_started_ns", None)
        if started_ns is None:
            return
        recorder.record((time.perf_counter_ns() - started_ns) / 1_000_000, statement)

    return recorder


def _measure_endpoint(client: TestClient, endpoint: dict[str, str], config: BenchmarkConfig, sql_recorder: SqlRecorder) -> dict[str, Any]:
    durations: list[float] = []
    sample_details: list[dict[str, Any]] = []
    total = config.warmup + config.samples
    for iteration in range(total):
        sql_recorder.start_endpoint(endpoint["name"])
        started = time.perf_counter_ns()
        try:
            response = client.request(endpoint["method"], endpoint["path"])
            elapsed_ms = (time.perf_counter_ns() - started) / 1_000_000
        finally:
            sql_recorder.stop_endpoint()
        if response.status_code != 200:
            pytest.fail(f"{endpoint['name']} returned HTTP {response.status_code}: {response.text}")
        _assert_endpoint_shape(endpoint["name"], response.json())
        if iteration >= config.warmup:
            durations.append(elapsed_ms)
            sql_sample = sql_recorder.samples_by_endpoint[endpoint["name"]][-1]
            sample_details.append(
                {
                    "iteration": iteration - config.warmup + 1,
                    "durationMs": round(elapsed_ms, 3),
                    "sqlCount": sql_sample["count"],
                    "sqlDurationMs": round(sql_sample["durationMs"], 3),
                    "appOverheadMs": round(elapsed_ms - sql_sample["durationMs"], 3),
                    "queries": sql_sample["queries"],
                }
            )
    return {
        "name": endpoint["name"],
        "method": endpoint["method"],
        "path": endpoint["path"],
        "status": 200,
        "count": len(durations),
        "minMs": round(min(durations), 3),
        "p50Ms": round(_percentile(durations, 50), 3),
        "p95Ms": round(_percentile(durations, 95), 3),
        "p99Ms": round(_percentile(durations, 99), 3),
        "maxMs": round(max(durations), 3),
        "slowestSamples": sorted(sample_details, key=lambda sample: sample["durationMs"], reverse=True)[:5],
        "sql": {
            "minCount": min(sample["sqlCount"] for sample in sample_details),
            "maxCount": max(sample["sqlCount"] for sample in sample_details),
            "maxDurationMs": max(sample["sqlDurationMs"] for sample in sample_details),
        },
    }


def _summarize_sql(statement: str) -> str:
    normalized = " ".join(statement.split())
    return normalized[:240]


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


def _percentile(values: list[float], percentile: int) -> float:
    ordered = sorted(values)
    index = max(math.ceil(len(ordered) * percentile / 100) - 1, 0)
    return ordered[index]


def _collect_query_plans(session: Session, targets: SeedTargets) -> list[dict[str, Any]]:
    target_day = datetime.fromisoformat(f"{targets.date}T00:00:00+00:00")
    target_month = datetime(target_day.year, target_day.month, 1, tzinfo=UTC)
    next_month = datetime(
        target_day.year + int(target_day.month == 12),
        1 if target_day.month == 12 else target_day.month + 1,
        1,
        tzinfo=UTC,
    )
    statements: list[tuple[str, Select[Any]]] = [
        (
            "recommended-first-page",
            select(model.Concert).order_by(model.Concert.created_at.desc(), model.Concert.id.desc()).limit(13),
        ),
        (
            "recommended-cursor-page",
            select(model.Concert)
            .where(model.Concert.created_at < targets.cursor_created_at)
            .order_by(model.Concert.created_at.desc(), model.Concert.id.desc())
            .limit(13),
        ),
        ("concert-detail-base", select(model.Concert).where(model.Concert.id == targets.concert_id)),
        ("concert-detail-showtimes", select(model.Showtime).where(model.Showtime.concert_id == targets.concert_id)),
        (
            "calendar-bookable-exists",
            select(model.Showtime.starts_at)
            .where(
                model.Showtime.concert_id == targets.concert_id,
                model.Showtime.starts_at >= target_month,
                model.Showtime.starts_at < next_month,
                model.Showtime.status.not_in(("closed", "canceled", "sold_out")),
                select(model.Seat.id)
                .where(
                    model.Seat.showtime_id == model.Showtime.id,
                    model.Seat.status == "sellable",
                )
                .exists(),
            )
            .order_by(model.Showtime.starts_at),
        ),
        (
            "date-performances",
            select(model.Showtime)
            .where(
                model.Showtime.concert_id == targets.concert_id,
                model.Showtime.starts_at >= target_day,
                model.Showtime.starts_at < target_day + timedelta(days=1),
            )
            .order_by(model.Showtime.starts_at),
        ),
        ("seat-map-showtime", select(model.Showtime).where(model.Showtime.id == targets.performance_id)),
        ("seat-map-seats", select(model.Seat).where(model.Seat.showtime_id == targets.performance_id)),
        ("seat-map-grades", select(model.SeatGrade).where(model.SeatGrade.showtime_id == targets.performance_id)),
    ]
    return [_explain_statement(session, label, statement) for label, statement in statements]


def _explain_statement(session: Session, label: str, statement: Select[Any]) -> dict[str, Any]:
    compiled = statement.compile(dialect=session.bind.dialect, compile_kwargs={"literal_binds": True})
    raw_plan = session.execute(text(f"EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) {compiled}")).scalar_one()
    plan_doc = raw_plan[0] if isinstance(raw_plan, list) else json.loads(raw_plan)[0]
    plan = plan_doc["Plan"]
    node_types: list[str] = []
    scan_nodes: list[dict[str, Any]] = []
    _walk_plan(plan, node_types, scan_nodes)
    has_seq_scan = any(node["nodeType"] == "Seq Scan" for node in scan_nodes)
    has_index_scan = any("Index" in node["nodeType"] or "Bitmap" in node["nodeType"] for node in scan_nodes)
    if has_seq_scan and has_index_scan:
        judgement = "mixed"
    elif has_index_scan:
        judgement = "index_scan"
    elif has_seq_scan:
        judgement = "seq_scan"
    else:
        judgement = "no_table_scan"
    return {
        "label": label,
        "judgement": judgement,
        "nodeTypes": node_types,
        "scanNodes": scan_nodes,
        "actualRows": plan.get("Actual Rows"),
        "sharedHitBlocks": plan.get("Shared Hit Blocks", 0),
        "sharedReadBlocks": plan.get("Shared Read Blocks", 0),
        "planningTimeMs": round(plan_doc["Planning Time"], 3),
        "executionTimeMs": round(plan_doc["Execution Time"], 3),
    }


def _walk_plan(plan: dict[str, Any], node_types: list[str], scan_nodes: list[dict[str, Any]]) -> None:
    node_type = plan["Node Type"]
    node_types.append(node_type)
    if "Scan" in node_type:
        scan_nodes.append(
            {
                "nodeType": node_type,
                "relation": plan.get("Relation Name"),
                "index": plan.get("Index Name"),
                "actualRows": plan.get("Actual Rows"),
                "planRows": plan.get("Plan Rows"),
                "sharedHitBlocks": plan.get("Shared Hit Blocks", 0),
                "sharedReadBlocks": plan.get("Shared Read Blocks", 0),
            }
        )
    for child in plan.get("Plans", []):
        _walk_plan(child, node_types, scan_nodes)


def _write_artifact(directory: Path, artifact: dict[str, Any], started_at: datetime) -> None:
    directory.mkdir(parents=True, exist_ok=True)
    timestamp = started_at.strftime("%Y%m%dT%H%M%SZ")
    output = json.dumps(artifact, indent=2, sort_keys=True)
    (directory / f"{timestamp}.json").write_text(output + "\n", encoding="utf-8")
    (directory / "latest.json").write_text(output + "\n", encoding="utf-8")


def _git_info(repo_root: Path) -> dict[str, Any]:
    env_head = os.environ.get("SERVICE_GIT_HEAD")
    if env_head is not None:
        return {
            "head": env_head,
            "dirty": os.environ.get("SERVICE_GIT_DIRTY") == "true",
            "statusShort": [],
        }
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
    }
