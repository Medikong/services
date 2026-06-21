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

from app.database import Base, get_db
from app.models import Payment, PaymentEvent
from app.routes.payments import router as payment_router


SERVICE_NAME = "payment-service"


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
    payment_id: str
    customer_id: str
    concert_id: str


@dataclass(frozen=True)
class EndpointCase:
    name: str
    method: str
    path: Callable[[int], str]
    status: int
    json_body: Callable[[int], dict[str, Any]] | None = None
    headers: Callable[[int], dict[str, str]] | None = None


def test_payment_api_benchmark_outputs_artifact(request: pytest.FixtureRequest) -> None:
    if not request.config.getoption("--run-payment-api-benchmark"):
        pytest.skip("use --run-payment-api-benchmark to run the isolated PostgreSQL benchmark")

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
        preset = load_preset(request.config.getoption("--payment-benchmark-preset"))
    except ValueError as exc:
        pytest.fail(str(exc))
    config = BenchmarkConfig(
        samples=request.config.getoption("--payment-benchmark-samples"),
        warmup=request.config.getoption("--payment-benchmark-warmup"),
        artifact_dir=Path(request.config.getoption("--payment-benchmark-artifact-dir")),
        preset=preset,
    )
    if config.samples < 1:
        pytest.fail("payment benchmark samples must be a positive integer")
    if config.warmup < 0:
        pytest.fail("payment benchmark warmup must be zero or greater")
    return config


def _benchmark_app(factory: sessionmaker[Session]) -> FastAPI:
    app = FastAPI(title=f"{SERVICE_NAME}-api-benchmark")
    app.include_router(payment_router)

    def override_get_db() -> Iterator[Session]:
        db = factory()
        try:
            yield db
        finally:
            db.close()

    app.dependency_overrides[get_db] = override_get_db
    return app


def _seed_dataset(session: Session, config: BenchmarkConfig) -> SeedTargets:
    tables = config.preset.service_tables(SERVICE_NAME)
    now = datetime(2026, 7, 1, 12, 0, tzinfo=UTC)
    concert_id = uuid_id("concert", "bench", 0)
    customer_id = user_id_for(SERVICE_NAME, "normal")
    for rows in chunked(_payment_rows(config, now, customer_id, concert_id)):
        session.execute(insert(Payment), rows)
    for rows in chunked(_payment_event_rows(tables["payment_events"], now)):
        session.execute(insert(PaymentEvent), rows)
    return SeedTargets(payment_id=uuid_id("payment", "bench", 0), customer_id=customer_id, concert_id=concert_id)


def _payment_rows(
    config: BenchmarkConfig,
    now: datetime,
    customer_id: str,
    concert_id: str,
) -> Iterator[dict[str, Any]]:
    total = config.preset.service_tables(SERVICE_NAME)["payments"]
    counts = config.preset.payment_status_counts
    for index in range(total):
        status = status_for_index(index, counts)
        yield {
            "id": uuid_id("payment", "bench", index),
            "reservation_id": uuid_id("reservation", "bench", index),
            "concert_id": concert_id if index % 7 == 0 else uuid_id("concert", "payment", index % config.preset.catalog["concerts"]),
            "user_id": customer_id if index == 0 else _payment_user_id(index, total, config),
            "amount": 50000 + (index % 10000),
            "method": "mock",
            "status": status,
            "idempotency_key": None,
            "approved_at": now + timedelta(seconds=index) if status == "approved" else None,
            "created_at": now - timedelta(seconds=index),
        }


def _payment_event_rows(total: int, now: datetime) -> Iterator[dict[str, Any]]:
    for index in range(total):
        status = "approved" if index % 13 else "failed"
        payment_id = uuid_id("payment", "bench", index)
        event_id = uuid_id("payment-event", "bench", index)
        yield {
            "id": event_id,
            "event_type": f"payment.{status}",
            "payment_id": payment_id,
            "payload": {
                "eventId": event_id,
                "paymentId": payment_id,
                "reservationId": uuid_id("reservation", "bench", index),
                "concertId": uuid_id("concert", "payment", index % 270),
                "sourceId": payment_id,
            },
            "trace_context": None,
            "publish_status": "pending",
            "published_at": None,
            "publish_attempts": 0,
            "last_publish_error": None,
            "created_at": now - timedelta(seconds=index),
        }


def _payment_user_id(index: int, total: int, config: BenchmarkConfig) -> str:
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
            name="create-payment",
            method="POST",
            path=lambda _: "/payments",
            status=201,
            json_body=lambda index: {
                "reservationId": uuid_id("reservation", "create", index),
                "concertId": targets.concert_id,
                "seatId": f"A-{index:06d}",
                "amount": 50000,
                "method": "mock",
                "simulation": "approve",
            },
            headers=lambda index: _auth_headers("CUSTOMER", f"bench-create-{index:06d}"),
        ),
        EndpointCase(
            name="get-payment",
            method="GET",
            path=lambda _: f"/payments/{targets.payment_id}",
            status=200,
            headers=lambda _: _auth_headers("CUSTOMER", targets.customer_id),
        ),
        EndpointCase(
            name="provider-settlement-basis",
            method="GET",
            path=lambda _: f"/provider/concerts/{targets.concert_id}/settlement-basis",
            status=200,
            headers=lambda _: _auth_headers("PROVIDER", "bench-provider"),
        ),
        EndpointCase(
            name="admin-settlement-basis",
            method="GET",
            path=lambda _: f"/admin/concerts/{targets.concert_id}/settlement-basis",
            status=200,
            headers=lambda _: _auth_headers("ADMIN", "bench-admin"),
        ),
    ]


def _auth_headers(role: str, user_id: str) -> dict[str, str]:
    return {
        "X-User-Id": user_id,
        "X-User-Email": f"{user_id}@benchmark.local",
        "X-User-Role": role,
        "X-Token-Id": f"token-{role.lower()}",
    }


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
    return [
        {
            "endpoint": "create-payment",
            "queries": [
                explain_postgres_sql(
                    session,
                    label="payment-idempotency-lookup",
                    sql="SELECT id FROM payments WHERE user_id = :user_id AND idempotency_key = :idempotency_key LIMIT 1",
                    params={"user_id": targets.customer_id, "idempotency_key": "explain-idempotency-key"},
                    query_shape="SELECT payments WHERE user_id AND idempotency_key, INSERT payments/events",
                    index_decision="idempotency key가 있는 운영 path에는 unique constraint를 유지한다.",
                    data_analysis=f"payments={tables['payments']:,}. benchmark 요청은 새 결제 insert/outbox insert 비용이 중심이다.",
                )
            ],
            "sampleInterpretation": sample_note,
        },
        {
            "endpoint": "get-payment",
            "queries": [
                explain_postgres_sql(
                    session,
                    label="payment-by-id",
                    sql="SELECT id, user_id, status FROM payments WHERE id = :payment_id",
                    params={"payment_id": targets.payment_id},
                    query_shape="SELECT payments WHERE id",
                    index_decision="단건 조회는 PK 유지.",
                    data_analysis="전체 payments 규모보다 권한 확인과 응답 직렬화 비용이 크다.",
                )
            ],
            "sampleInterpretation": sample_note,
        },
        {
            "endpoint": "provider/admin-settlement-basis",
            "queries": [
                explain_postgres_sql(
                    session,
                    label="settlement-aggregate-approved",
                    sql="SELECT coalesce(sum(amount), 0), count(*) FROM payments WHERE concert_id = :concert_id AND status = 'approved'",
                    params={"concert_id": targets.concert_id},
                    query_shape="SUM/COUNT payments WHERE concert_id AND status='approved'",
                    index_decision="정산 집계 predicate에 맞춰 (concert_id, status) 복합 index를 사용한다.",
                    data_analysis=f"payments={tables['payments']:,}, approved={config.preset.payment_status_counts['approved']:,}. sum/count를 한 번의 aggregate query로 가져와 같은 row set을 두 번 훑지 않는다.",
                ),
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
