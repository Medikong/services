#!/usr/bin/env python3
"""Run isolated Docker Compose idle benchmarks and persist reproducible reports."""

from __future__ import annotations

import argparse
import json
import math
import os
import platform
import re
import secrets
import shutil
import subprocess
import sys
import tempfile
import time
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Iterable, Sequence


IDLE_DIR = Path(__file__).resolve().parent
REPO_ROOT = IDLE_DIR.parents[2]
COMPOSE_FILE = IDLE_DIR / "docker-compose.yml"
RUNS_DIR = IDLE_DIR / "runs"
SCHEMA_VERSION = "1.0"
DEFAULT_SERVICES = (
    "auth-service",
    "user-service",
    "catalog-service",
    "coupon-service",
    "interest-service",
    "order-service",
    "payment-service",
    "notification-service",
    "dropmong-web",
)
ALIASES = {name.removesuffix("-service"): name for name in DEFAULT_SERVICES}
ALIASES["web"] = "dropmong-web"

SERVICE_PLANS: dict[str, dict[str, Any]] = {
    "auth-service": {
        "start": ["auth-service", "auth-worker"],
        "readiness": ["http://auth-service:9090", "http://auth-worker:9092"],
        "components": {
            "auth-service": "app",
            "auth-worker": "worker",
            "postgres": "database",
            "redis": "cache",
            "kafka": "broker",
            "idle-observer": "observer",
        },
    },
    "user-service": {
        "start": ["user-service"],
        "readiness": ["http://user-service:9090"],
        "components": {
            "user-service": "app",
            "postgres": "database",
            "idle-observer": "observer",
        },
    },
    "catalog-service": {
        "start": ["catalog-service"],
        "readiness": ["http://catalog-service:8081"],
        "components": {
            "catalog-service": "app",
            "postgres": "database",
            "kafka": "broker",
            "idle-observer": "observer",
        },
    },
    "coupon-service": {
        "start": ["coupon-service", "coupon-worker"],
        "readiness": ["http://coupon-service:9090", "http://coupon-worker:9092"],
        "components": {
            "coupon-service": "app",
            "coupon-worker": "worker",
            "postgres": "database",
            "idle-observer": "observer",
        },
    },
    "interest-service": {
        "start": ["interest-service"],
        "readiness": ["http://interest-service:8085"],
        "components": {
            "interest-service": "app",
            "postgres": "database",
            "idle-observer": "observer",
        },
    },
    "order-service": {
        "start": ["order-service"],
        "readiness": ["http://order-service:8082"],
        "components": {
            "order-service": "app",
            "postgres": "database",
            "kafka": "broker",
            "idle-observer": "observer",
        },
    },
    "payment-service": {
        "start": ["payment-service"],
        "readiness": ["http://payment-service:8083"],
        "components": {
            "payment-service": "app",
            "postgres": "database",
            "kafka": "broker",
            "idle-observer": "observer",
        },
    },
    "notification-service": {
        "start": ["notification-service"],
        "readiness": ["http://notification-service:8084"],
        "components": {
            "notification-service": "app",
            "postgres": "database",
            "kafka": "broker",
            "idle-observer": "observer",
        },
    },
    "dropmong-web": {
        "start": ["dropmong-web"],
        "readiness": ["http://dropmong-web:3000"],
        "components": {
            "dropmong-web": "app",
            "idle-observer": "observer",
        },
    },
}

SIZE_UNITS = {
    "b": 1,
    "kb": 1_000,
    "mb": 1_000**2,
    "gb": 1_000**3,
    "tb": 1_000**4,
    "kib": 1024,
    "mib": 1024**2,
    "gib": 1024**3,
    "tib": 1024**4,
}
SUMMARY_METRICS = (
    "cpu_percent",
    "cpu_cores",
    "memory_usage_bytes",
    "memory_limit_bytes",
    "memory_percent",
    "pids",
    "network_rx_bytes",
    "network_tx_bytes",
    "block_read_bytes",
    "block_write_bytes",
)


class BenchmarkError(RuntimeError):
    pass


class CommandError(BenchmarkError):
    def __init__(self, command: Sequence[str], returncode: int, output: str):
        safe_command = " ".join(command)
        tail = output.strip()[-4000:]
        super().__init__(f"명령 실패({returncode}): {safe_command}\n{tail}")
        self.returncode = returncode
        self.output = tail


def utc_now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")


def run_command(
    command: Sequence[str],
    *,
    cwd: Path = REPO_ROOT,
    env: dict[str, str] | None = None,
    timeout: float | None = None,
    check: bool = True,
) -> subprocess.CompletedProcess[str]:
    merged_env = os.environ.copy()
    if env:
        merged_env.update(env)
    completed = subprocess.run(
        list(command),
        cwd=cwd,
        env=merged_env,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        timeout=timeout,
        check=False,
    )
    if check and completed.returncode != 0:
        raise CommandError(command, completed.returncode, completed.stdout)
    return completed


def write_json(path: Path, document: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(json.dumps(document, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    temporary.replace(path)


def parse_size(value: str) -> int:
    normalized = value.strip().replace(" ", "")
    match = re.fullmatch(r"([0-9]+(?:\.[0-9]+)?)([A-Za-z]+)", normalized)
    if not match:
        raise ValueError(f"크기 값을 해석할 수 없습니다: {value!r}")
    amount, unit = match.groups()
    multiplier = SIZE_UNITS.get(unit.lower())
    if multiplier is None:
        raise ValueError(f"지원하지 않는 크기 단위입니다: {unit}")
    return int(float(amount) * multiplier)


def parse_pair(value: str) -> tuple[int, int]:
    parts = [part.strip() for part in value.split("/")]
    if len(parts) != 2:
        raise ValueError(f"입출력 쌍을 해석할 수 없습니다: {value!r}")
    return parse_size(parts[0]), parse_size(parts[1])


def parse_percentage(value: str) -> float:
    return float(value.strip().removesuffix("%"))


def parse_stats_line(line: str) -> dict[str, Any]:
    source = json.loads(line)
    memory_usage, memory_limit = parse_pair(source["MemUsage"])
    network_rx, network_tx = parse_pair(source["NetIO"])
    block_read, block_write = parse_pair(source["BlockIO"])
    cpu_percent = parse_percentage(source["CPUPerc"])
    return {
        "container_id": source.get("ID") or source.get("Container"),
        "container_name": source.get("Name"),
        "cpu_percent": cpu_percent,
        "cpu_cores": cpu_percent / 100.0,
        "memory_usage_bytes": memory_usage,
        "memory_limit_bytes": memory_limit,
        "memory_percent": parse_percentage(source["MemPerc"]),
        "pids": int(source["PIDs"]),
        "network_rx_bytes": network_rx,
        "network_tx_bytes": network_tx,
        "block_read_bytes": block_read,
        "block_write_bytes": block_write,
        "raw": {
            "cpu": source["CPUPerc"],
            "memory": source["MemUsage"],
            "memory_percent": source["MemPerc"],
            "network_io": source["NetIO"],
            "block_io": source["BlockIO"],
            "pids": source["PIDs"],
        },
    }


def percentile(values: Sequence[float | int], quantile: float) -> float:
    if not values:
        raise ValueError("percentile에는 한 개 이상의 값이 필요합니다")
    if not 0 <= quantile <= 1:
        raise ValueError("quantile은 0과 1 사이여야 합니다")
    ordered = sorted(float(value) for value in values)
    position = (len(ordered) - 1) * quantile
    lower = math.floor(position)
    upper = math.ceil(position)
    if lower == upper:
        return ordered[lower]
    weight = position - lower
    return ordered[lower] * (1 - weight) + ordered[upper] * weight


def summarize_values(values: Sequence[float | int]) -> dict[str, float]:
    return {
        "mean": round(sum(values) / len(values), 6),
        "p50": round(percentile(values, 0.50), 6),
        "p95": round(percentile(values, 0.95), 6),
        "max": round(max(values), 6),
    }


def summarize_components(samples: Sequence[dict[str, Any]]) -> dict[str, Any]:
    component_samples: dict[str, list[dict[str, Any]]] = {}
    for sample in samples:
        for component in sample.get("components", []):
            component_samples.setdefault(component["compose_service"], []).append(component)

    summaries: dict[str, Any] = {}
    for compose_service, values in component_samples.items():
        first = values[0]
        metrics = {
            metric: summarize_values([value[metric] for value in values])
            for metric in SUMMARY_METRICS
        }
        summaries[compose_service] = {
            "role": first["role"],
            "sample_count": len(values),
            "container_id": first["container_id"],
            "container_name": first["container_name"],
            "metrics": metrics,
        }
    return summaries


def normalize_services(raw: str | None) -> list[str]:
    if not raw or not raw.strip():
        return list(DEFAULT_SERVICES)
    requested = re.split(r"[\s,]+", raw.strip())
    selected: list[str] = []
    for item in requested:
        normalized = ALIASES.get(item, item)
        if normalized not in SERVICE_PLANS:
            raise BenchmarkError(f"알 수 없는 서비스입니다: {item}")
        if normalized not in selected:
            selected.append(normalized)
    return selected


def make_run_id() -> str:
    timestamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    return f"idle-{timestamp}-{secrets.token_hex(3)}"


def validate_run_id(run_id: str) -> None:
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]{0,79}", run_id):
        raise BenchmarkError("run ID는 영문자, 숫자, 점, 밑줄, 하이픈만 사용할 수 있습니다")


def project_name(run_id: str, service: str) -> str:
    compact_run = re.sub(r"[^a-z0-9]", "", run_id.lower())[-20:]
    compact_service = service.replace("-service", "").replace("dropmong-", "")
    return f"dmidle-{compact_run}-{compact_service}"[:63]


def compose_command(project: str, *arguments: str) -> list[str]:
    return ["docker", "compose", "-p", project, "-f", str(COMPOSE_FILE), *arguments]


def parse_json_output(output: str) -> Any:
    stripped = output.strip()
    if not stripped:
        return []
    try:
        return json.loads(stripped)
    except json.JSONDecodeError:
        documents = []
        for line in stripped.splitlines():
            try:
                documents.append(json.loads(line))
            except json.JSONDecodeError:
                return {"unparsed": stripped[-4000:]}
        return documents


def compose_state(project: str, env: dict[str, str]) -> Any:
    result = run_command(
        compose_command(project, "ps", "--all", "--format", "json"),
        env=env,
        check=False,
        timeout=30,
    )
    return {
        "exit_code": result.returncode,
        "containers": parse_json_output(result.stdout),
    }


def compose_logs(project: str, env: dict[str, str]) -> str:
    result = run_command(
        compose_command(project, "logs", "--no-color", "--tail", "80"),
        env=env,
        check=False,
        timeout=30,
    )
    return result.stdout[-12000:]


def wait_for_readiness(observer: str, bases: Sequence[str], timeout: int) -> None:
    deadline = time.monotonic() + timeout
    last_output = ""
    while time.monotonic() < deadline:
        ready = True
        for base in bases:
            result = run_command(
                [
                    "docker",
                    "exec",
                    observer,
                    "curl",
                    "--fail",
                    "--silent",
                    "--max-time",
                    "2",
                    f"{base}/readyz",
                ],
                check=False,
                timeout=10,
            )
            if result.returncode != 0:
                ready = False
                last_output = result.stdout[-1000:]
                break
        if ready:
            return
        time.sleep(2)
    raise BenchmarkError(f"readiness 확인 시간이 {timeout}초를 넘었습니다: {last_output.strip()}")


def container_id(project: str, compose_service: str, env: dict[str, str]) -> str:
    result = run_command(
        compose_command(project, "ps", "-q", compose_service),
        env=env,
        timeout=30,
    )
    value = result.stdout.strip()
    if not value:
        raise BenchmarkError(f"실행 중인 컨테이너를 찾을 수 없습니다: {compose_service}")
    return value.splitlines()[0]


def inspect_container(container: str) -> dict[str, Any]:
    result = run_command(["docker", "inspect", container], timeout=30)
    document = json.loads(result.stdout)[0]
    image_id = document["Image"]
    image_result = run_command(["docker", "image", "inspect", image_id], timeout=30, check=False)
    digests: list[str] = []
    if image_result.returncode == 0:
        image_document = json.loads(image_result.stdout)[0]
        digests = image_document.get("RepoDigests") or []
    return {
        "container_id": document["Id"],
        "container_name": document["Name"].lstrip("/"),
        "compose_service": document.get("Config", {}).get("Labels", {}).get("com.docker.compose.service"),
        "image_reference": document.get("Config", {}).get("Image"),
        "image_id": image_id,
        "image_digests": digests,
        "platform": document.get("Platform"),
        "started_at": document.get("State", {}).get("StartedAt"),
    }


def running_state(container: str) -> dict[str, Any]:
    result = run_command(["docker", "inspect", container], timeout=30, check=False)
    if result.returncode != 0:
        return {"running": False, "inspect_error": result.stdout[-1000:]}
    state = json.loads(result.stdout)[0].get("State", {})
    return {
        "running": state.get("Running"),
        "status": state.get("Status"),
        "exit_code": state.get("ExitCode"),
        "error": state.get("Error"),
        "health": state.get("Health", {}).get("Status"),
    }


def collect_sample(
    containers: dict[str, str], roles: dict[str, str]
) -> dict[str, Any]:
    result = run_command(
        ["docker", "stats", "--no-stream", "--format", "{{json .}}", *containers.values()],
        timeout=30,
    )
    parsed_by_id: dict[str, dict[str, Any]] = {}
    for line in result.stdout.splitlines():
        if not line.strip():
            continue
        parsed = parse_stats_line(line)
        parsed_by_id[str(parsed["container_id"])] = parsed

    components = []
    for compose_service, expected_id in containers.items():
        parsed = next(
            (
                value
                for observed_id, value in parsed_by_id.items()
                if expected_id.startswith(observed_id) or observed_id.startswith(expected_id[:12])
            ),
            None,
        )
        if parsed is None:
            raise BenchmarkError(f"docker stats에 {compose_service} 표본이 없습니다")
        parsed["compose_service"] = compose_service
        parsed["role"] = roles[compose_service]
        components.append(parsed)
    return {"timestamp": utc_now(), "components": components}


def sleep_until(deadline: float) -> None:
    remaining = deadline - time.monotonic()
    if remaining > 0:
        time.sleep(remaining)


def sample_for_duration(
    containers: dict[str, str],
    roles: dict[str, str],
    measure_seconds: int,
    interval_seconds: int,
) -> tuple[list[dict[str, Any]], dict[str, Any]]:
    samples: list[dict[str, Any]] = []
    started = time.monotonic()
    sample_count = max(1, math.ceil(measure_seconds / interval_seconds))
    for index in range(sample_count):
        sleep_until(started + index * interval_seconds)
        samples.append(collect_sample(containers, roles))
    sleep_until(started + measure_seconds)
    wall_duration = time.monotonic() - started
    timing = analyze_sample_timing(samples, measure_seconds, interval_seconds, wall_duration)
    return samples, timing


def analyze_sample_timing(
    samples: Sequence[dict[str, Any]],
    measure_seconds: int,
    interval_seconds: int,
    wall_duration_seconds: float,
) -> dict[str, Any]:
    timestamps = [
        datetime.fromisoformat(sample["timestamp"].replace("Z", "+00:00"))
        for sample in samples
    ]
    gaps = [
        (current - previous).total_seconds()
        for previous, current in zip(timestamps, timestamps[1:])
    ]
    maximum_gap = max(gaps, default=0.0)
    expected_sample_count = max(1, math.ceil(measure_seconds / interval_seconds))
    observed_span = (timestamps[-1] - timestamps[0]).total_seconds() if len(timestamps) > 1 else 0.0
    maximum_allowed_gap = interval_seconds * 2
    maximum_allowed_duration = measure_seconds + interval_seconds * 2
    continuous = (
        len(samples) == expected_sample_count
        and maximum_gap <= maximum_allowed_gap
        and wall_duration_seconds <= maximum_allowed_duration
    )
    return {
        "expected_sample_count": expected_sample_count,
        "observed_sample_count": len(samples),
        "expected_interval_seconds": interval_seconds,
        "observed_span_seconds": round(observed_span, 6),
        "wall_duration_seconds": round(wall_duration_seconds, 6),
        "mean_gap_seconds": round(sum(gaps) / len(gaps), 6) if gaps else 0.0,
        "maximum_gap_seconds": round(maximum_gap, 6),
        "maximum_allowed_gap_seconds": maximum_allowed_gap,
        "continuous": continuous,
    }


def build_failure_record(service: str, error: Exception, last_state: Any) -> dict[str, Any]:
    return {
        "service": service,
        "status": "failed",
        "error": {
            "type": type(error).__name__,
            "message": str(error)[-5000:],
        },
        "last_state": last_state,
    }


def build_service_summary(raw: dict[str, Any], metadata: dict[str, Any]) -> dict[str, Any]:
    result = {
        "schema_version": SCHEMA_VERSION,
        "run_id": raw["run_id"],
        "service": raw["service"],
        "status": raw["status"],
        "project": raw["project"],
        "data_state": raw["data_state"],
        "sample_count": len(raw["samples"]),
        "sampling": raw.get("sampling"),
        "components": summarize_components(raw["samples"]) if raw["samples"] else {},
        "container_metadata": metadata,
        "error": raw.get("error"),
        "last_state": raw.get("last_state"),
        "cleanup": raw.get("cleanup"),
    }
    return result


def cleanup_project(project: str, env: dict[str, str]) -> dict[str, Any]:
    messages = []
    result = run_command(
        compose_command(
            project,
            "--profile",
            "observer",
            "down",
            "-v",
            "--remove-orphans",
            "--timeout",
            "20",
        ),
        env=env,
        check=False,
        timeout=120,
    )
    messages.append(result.stdout.strip()[-3000:])

    container_result = run_command(
        ["docker", "ps", "-aq", "--filter", f"label=com.docker.compose.project={project}"],
        check=False,
        timeout=30,
    )
    remaining_containers = container_result.stdout.split()
    remove_exit_code = 0
    if remaining_containers:
        remove_result = run_command(
            ["docker", "rm", "-f", *remaining_containers],
            check=False,
            timeout=60,
        )
        remove_exit_code = remove_result.returncode
        messages.append(remove_result.stdout.strip()[-1000:])
        second_down = run_command(
            compose_command(
                project,
                "--profile",
                "observer",
                "down",
                "-v",
                "--remove-orphans",
                "--timeout",
                "20",
            ),
            env=env,
            check=False,
            timeout=120,
        )
        messages.append(second_down.stdout.strip()[-2000:])

    final_containers = run_command(
        ["docker", "ps", "-aq", "--filter", f"label=com.docker.compose.project={project}"],
        check=False,
        timeout=30,
    ).stdout.split()
    final_volumes = run_command(
        ["docker", "volume", "ls", "-q", "--filter", f"label=com.docker.compose.project={project}"],
        check=False,
        timeout=30,
    ).stdout.split()
    final_networks = run_command(
        ["docker", "network", "ls", "-q", "--filter", f"label=com.docker.compose.project={project}"],
        check=False,
        timeout=30,
    ).stdout.split()
    passed = (
        result.returncode == 0
        and remove_exit_code == 0
        and not final_containers
        and not final_volumes
        and not final_networks
    )
    return {
        "status": "passed" if passed else "failed",
        "exit_code": result.returncode,
        "remaining": {
            "containers": final_containers,
            "volumes": final_volumes,
            "networks": final_networks,
        },
        "message": "\n".join(message for message in messages if message)[-5000:],
    }


def run_service(
    service: str,
    run_id: str,
    run_dir: Path,
    key_file: Path,
    warmup_seconds: int,
    measure_seconds: int,
    interval_seconds: int,
    readiness_timeout: int,
) -> dict[str, Any]:
    plan = SERVICE_PLANS[service]
    project = project_name(run_id, service)
    env = {
        "IDLE_AUTH_JWT_PRIVATE_KEY_FILE": str(key_file),
        "IDLE_OBSERVE_TARGETS": " ".join(plan["readiness"]),
        "IDLE_OPERATIONAL_SCRAPE_INTERVAL_SECONDS": "15",
    }
    raw = {
        "schema_version": SCHEMA_VERSION,
        "run_id": run_id,
        "service": service,
        "project": project,
        "status": "running",
        "started_at": utc_now(),
        "finished_at": None,
        "data_state": {
            "profile": "schema_only",
            "business_rows_seeded": False,
            "description": "새 볼륨에 서비스 마이그레이션만 적용했으며 업무 fixture는 적재하지 않음",
        },
        "samples": [],
    }
    metadata: dict[str, Any] = {}
    containers: dict[str, str] = {}
    error: Exception | None = None

    print(f"[{service}] 이미지 빌드와 컨테이너 시작", flush=True)
    try:
        run_command(
            compose_command(project, "up", "-d", "--build", *plan["start"]),
            env=env,
            timeout=1800,
        )
        run_command(compose_command(project, "up", "-d", "idle-observer"), env=env, timeout=180)
        observer = container_id(project, "idle-observer", env)
        wait_for_readiness(observer, plan["readiness"], readiness_timeout)

        for compose_service in plan["components"]:
            containers[compose_service] = container_id(project, compose_service, env)
            metadata[compose_service] = inspect_container(containers[compose_service])

        print(f"[{service}] 준비 완료, {warmup_seconds}초 warmup", flush=True)
        time.sleep(warmup_seconds)
        print(
            f"[{service}] {measure_seconds}초 측정, {interval_seconds}초 간격",
            flush=True,
        )
        raw["samples"], raw["sampling"] = sample_for_duration(
            containers,
            plan["components"],
            measure_seconds,
            interval_seconds,
        )
        if not raw["sampling"]["continuous"]:
            raise BenchmarkError(
                "표본 수집이 연속 측정 조건을 벗어났습니다: "
                f"최대 간격 {raw['sampling']['maximum_gap_seconds']}초, "
                f"전체 {raw['sampling']['wall_duration_seconds']}초"
            )
        component_states = {name: running_state(value) for name, value in containers.items()}
        raw["last_state"] = component_states
        stopped = [name for name, state in component_states.items() if not state.get("running")]
        if stopped:
            raise BenchmarkError(f"측정 중 컨테이너가 중지되었습니다: {', '.join(stopped)}")
        raw["status"] = "passed"
    except Exception as caught:  # Failure is recorded and remaining services continue.
        error = caught
        raw.update(build_failure_record(service, caught, compose_state(project, env)))
        raw["logs_tail"] = compose_logs(project, env)
    finally:
        cleanup = cleanup_project(project, env)
        raw["cleanup"] = cleanup
        if cleanup["status"] == "failed" and error is None:
            cleanup_error = BenchmarkError("벤치마크 소유 Docker 자원 정리에 실패했습니다")
            raw.update(build_failure_record(service, cleanup_error, raw.get("last_state")))
        raw["finished_at"] = utc_now()
        write_json(run_dir / "raw" / f"{service}.json", raw)
        summary = build_service_summary(raw, metadata)
        write_json(run_dir / "services" / f"{service}.json", summary)

    print(f"[{service}] {raw['status']}", flush=True)
    return summary


def git_metadata() -> dict[str, Any]:
    sha = run_command(["git", "rev-parse", "HEAD"]).stdout.strip()
    status = run_command(["git", "status", "--porcelain"]).stdout
    branch = run_command(["git", "branch", "--show-current"]).stdout.strip()
    return {
        "root": str(REPO_ROOT),
        "sha": sha,
        "branch": branch,
        "dirty": bool(status.strip()),
    }


def environment_metadata() -> dict[str, Any]:
    docker_version = parse_json_output(
        run_command(["docker", "version", "--format", "{{json .}}"], timeout=30).stdout
    )
    docker_info = parse_json_output(
        run_command(["docker", "info", "--format", "{{json .}}"], timeout=30).stdout
    )
    compose_version = run_command(["docker", "compose", "version"], timeout=30).stdout.strip()
    return {
        "docker": {
            "version": docker_version,
            "compose_version": compose_version,
        },
        "host": {
            "platform": platform.platform(),
            "machine": platform.machine(),
            "logical_cpu_count": os.cpu_count(),
            "docker_cpu_count": docker_info.get("NCPU") if isinstance(docker_info, dict) else None,
            "docker_memory_bytes": docker_info.get("MemTotal") if isinstance(docker_info, dict) else None,
            "docker_operating_system": docker_info.get("OperatingSystem") if isinstance(docker_info, dict) else None,
            "docker_architecture": docker_info.get("Architecture") if isinstance(docker_info, dict) else None,
        },
    }


def validate_execution_document(document: dict[str, Any]) -> None:
    required = {
        "schema_version",
        "run_id",
        "status",
        "started_at",
        "finished_at",
        "repository",
        "environment",
        "configuration",
        "data_state",
        "services",
    }
    missing = sorted(required - document.keys())
    if missing:
        raise ValueError(f"execution metadata 필드가 없습니다: {', '.join(missing)}")
    if document["status"] not in {"running", "passed", "failed"}:
        raise ValueError("execution status가 올바르지 않습니다")
    configuration_required = {
        "warmup_seconds",
        "measure_seconds",
        "sample_interval_seconds",
        "operational_scrape_interval_seconds",
    }
    missing_configuration = sorted(configuration_required - document["configuration"].keys())
    if missing_configuration:
        raise ValueError(f"execution configuration 필드가 없습니다: {', '.join(missing_configuration)}")


def build_summary(run_id: str, service_results: Sequence[dict[str, Any]]) -> dict[str, Any]:
    passed = [result for result in service_results if result["status"] == "passed"]
    failed = [result for result in service_results if result["status"] == "failed"]
    comparison = []
    for result in passed:
        app = next(
            (component for component in result["components"].values() if component["role"] == "app"),
            None,
        )
        if app:
            comparison.append(
                {
                    "service": result["service"],
                    "sample_count": app["sample_count"],
                    "cpu_percent": app["metrics"]["cpu_percent"],
                    "cpu_cores": app["metrics"]["cpu_cores"],
                    "memory_usage_bytes": app["metrics"]["memory_usage_bytes"],
                    "memory_percent": app["metrics"]["memory_percent"],
                    "pids": app["metrics"]["pids"],
                }
            )
    comparison.sort(key=lambda value: value["memory_usage_bytes"]["mean"], reverse=True)
    return {
        "schema_version": SCHEMA_VERSION,
        "run_id": run_id,
        "status": "passed" if not failed else "failed",
        "service_count": len(service_results),
        "passed_count": len(passed),
        "failed_count": len(failed),
        "failed_services": [result["service"] for result in failed],
        "app_comparison": comparison,
        "services": {result["service"]: result for result in service_results},
    }


def mib(value: float) -> float:
    return value / 1024 / 1024


def build_analysis(execution: dict[str, Any], summary: dict[str, Any]) -> str:
    lines = [
        f"# Idle benchmark 분석: {execution['run_id']}",
        "",
        "## 실행 조건",
        "",
        f"- 상태: `{summary['status']}` ({summary['passed_count']}개 성공, {summary['failed_count']}개 실패)",
        f"- Git: `{execution['repository']['sha']}` (dirty: `{str(execution['repository']['dirty']).lower()}`)",
        f"- 시간: warmup {execution['configuration']['warmup_seconds']}초, 측정 {execution['configuration']['measure_seconds']}초, 표본 간격 {execution['configuration']['sample_interval_seconds']}초",
        "- 데이터: 새 데이터베이스에 마이그레이션만 적용한 `schema_only`; 업무 데이터 적재 없음",
        "- 활동: 업무 API 요청 없음, readiness·healthcheck·metrics scrape와 내부 polling 포함",
        "",
        "## 앱 컨테이너 비교",
        "",
        "| 서비스 | 표본 | CPU 평균 | CPU p95 | 메모리 평균 | 메모리 p95 | 메모리 최대 |",
        "| --- | ---: | ---: | ---: | ---: | ---: | ---: |",
    ]
    for row in summary["app_comparison"]:
        cpu = row["cpu_percent"]
        memory = row["memory_usage_bytes"]
        lines.append(
            f"| {row['service']} | {row['sample_count']} | {cpu['mean']:.3f}% | {cpu['p95']:.3f}% | "
            f"{mib(memory['mean']):.2f} MiB | {mib(memory['p95']):.2f} MiB | {mib(memory['max']):.2f} MiB |"
        )

    worker_rows = []
    for service, result in summary["services"].items():
        for component_name, component in result.get("components", {}).items():
            if component["role"] == "worker":
                worker_rows.append((service, component_name, component))
    lines.extend(["", "## Worker 비용", ""])
    if worker_rows:
        lines.extend(
            [
                "| 서비스 | worker | CPU 평균 | CPU p95 | 메모리 평균 | 메모리 p95 |",
                "| --- | --- | ---: | ---: | ---: | ---: |",
            ]
        )
        for service, name, component in worker_rows:
            cpu = component["metrics"]["cpu_percent"]
            memory = component["metrics"]["memory_usage_bytes"]
            lines.append(
                f"| {service} | {name} | {cpu['mean']:.3f}% | {cpu['p95']:.3f}% | "
                f"{mib(memory['mean']):.2f} MiB | {mib(memory['p95']):.2f} MiB |"
            )
    else:
        lines.append("성공한 결과에 별도 worker 컨테이너가 없다.")

    lines.extend(["", "## 관찰", ""])
    if summary["app_comparison"]:
        highest_memory = summary["app_comparison"][0]
        highest_cpu = max(summary["app_comparison"], key=lambda item: item["cpu_percent"]["mean"])
        lines.append(
            f"- 앱 메모리 평균이 가장 큰 서비스는 `{highest_memory['service']}`이며 "
            f"{mib(highest_memory['memory_usage_bytes']['mean']):.2f} MiB다."
        )
        lines.append(
            f"- 앱 CPU 평균이 가장 큰 서비스는 `{highest_cpu['service']}`이며 "
            f"{highest_cpu['cpu_percent']['mean']:.3f}%다."
        )
        outliers = []
        for row in summary["app_comparison"]:
            cpu = row["cpu_percent"]
            if cpu["max"] > max(cpu["p95"] * 2, cpu["mean"] + 0.5):
                outliers.append(row["service"])
        if outliers:
            lines.append(f"- CPU 최대값이 p95보다 크게 튄 앱: `{', '.join(outliers)}`. 원시 timestamp를 함께 확인해야 한다.")
        else:
            lines.append("- 앱 CPU 최대값이 p95의 두 배를 넘는 뚜렷한 순간 이상치는 확인되지 않았다.")
    if worker_rows:
        lines.append("- Auth와 Coupon의 background polling 비용은 앱과 합치지 않고 worker 표에 따로 표시했다.")
    lines.append("- PostgreSQL·Redis·Kafka·observer는 서비스 비교에서 제외했지만 서비스별 JSON에 별도 구성 요소로 남겼다.")

    if summary["failed_services"]:
        lines.extend(["", "## 실패", ""])
        for service in summary["failed_services"]:
            error = summary["services"][service].get("error") or {}
            lines.append(f"- `{service}`: {error.get('message', '원인 미기록')}")

    lines.extend(
        [
            "",
            "## 해석 제한",
            "",
            "이 결과는 이 호스트, 이 이미지, 빈 데이터베이스에서 손님이 없는 동안 쓴 전기량과 비슷하다. "
            "가게가 손님을 몇 명까지 받을 수 있는지는 알려 주지 않는다. API 처리 용량은 180일 데이터 적재와 별도 부하 테스트로 확인해야 한다.",
            "",
        ]
    )
    return "\n".join(lines)


def generate_rsa_key(path: Path) -> None:
    if shutil.which("openssl") is None:
        raise BenchmarkError("Auth 벤치마크용 임시 RSA 키를 만들 openssl이 없습니다")
    run_command(
        [
            "openssl",
            "genpkey",
            "-algorithm",
            "RSA",
            "-pkeyopt",
            "rsa_keygen_bits:2048",
            "-out",
            str(path),
        ],
        timeout=60,
    )
    path.chmod(0o600)


def positive_integer(value: str) -> int:
    parsed = int(value)
    if parsed <= 0:
        raise argparse.ArgumentTypeError("0보다 큰 정수여야 합니다")
    return parsed


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="서비스별 컨테이너 idle 자원을 측정합니다.")
    parser.add_argument("--services", default="", help="공백 또는 쉼표로 구분한 서비스 목록")
    parser.add_argument("--warmup-seconds", type=positive_integer, default=60)
    parser.add_argument("--measure-seconds", type=positive_integer, default=180)
    parser.add_argument("--sample-interval-seconds", type=positive_integer, default=5)
    parser.add_argument("--readiness-timeout-seconds", type=positive_integer, default=180)
    parser.add_argument("--run-id", default=None)
    return parser.parse_args(argv)


def run_benchmark(args: argparse.Namespace) -> tuple[int, Path]:
    services = normalize_services(args.services)
    run_id = args.run_id or make_run_id()
    validate_run_id(run_id)
    run_dir = RUNS_DIR / run_id
    if run_dir.exists():
        raise BenchmarkError(f"이미 존재하는 run ID입니다: {run_id}")
    run_dir.mkdir(parents=True)

    execution = {
        "schema_version": SCHEMA_VERSION,
        "run_id": run_id,
        "status": "running",
        "started_at": utc_now(),
        "finished_at": None,
        "repository": git_metadata(),
        "environment": environment_metadata(),
        "configuration": {
            "services": services,
            "warmup_seconds": args.warmup_seconds,
            "measure_seconds": args.measure_seconds,
            "sample_interval_seconds": args.sample_interval_seconds,
            "operational_scrape_interval_seconds": 15,
            "app_healthcheck_interval": os.environ.get("IDLE_APP_HEALTHCHECK_INTERVAL", "5s"),
            "readiness_timeout_seconds": args.readiness_timeout_seconds,
            "compose_file": str(COMPOSE_FILE.relative_to(REPO_ROOT)),
            "isolation": "one target service at a time; app and its dedicated worker measured together",
        },
        "data_state": {
            "profile": "schema_only",
            "business_rows_seeded": False,
            "dataset_document": "tests/benchmarks/datasets/baseline-180days.md",
        },
        "services": {},
    }
    validate_execution_document(execution)
    write_json(run_dir / "execution.json", execution)

    service_results: list[dict[str, Any]] = []
    with tempfile.TemporaryDirectory(prefix="dropmong-idle-") as temporary_directory:
        key_file = Path(temporary_directory) / "auth-idle-jwt.pem"
        if "auth-service" in services:
            generate_rsa_key(key_file)
        else:
            key_file = Path("/dev/null")
        for service in services:
            result = run_service(
                service,
                run_id,
                run_dir,
                key_file,
                args.warmup_seconds,
                args.measure_seconds,
                args.sample_interval_seconds,
                args.readiness_timeout_seconds,
            )
            service_results.append(result)
            execution["services"][service] = {
                "status": result["status"],
                "project": result["project"],
                "sample_count": result["sample_count"],
                "error": result.get("error"),
                "images": {
                    name: {
                        "image_reference": metadata.get("image_reference"),
                        "image_id": metadata.get("image_id"),
                        "image_digests": metadata.get("image_digests"),
                    }
                    for name, metadata in result.get("container_metadata", {}).items()
                },
            }
            write_json(run_dir / "execution.json", execution)

    summary = build_summary(run_id, service_results)
    write_json(run_dir / "summary.json", summary)
    (run_dir / "analysis.md").write_text(build_analysis(execution, summary), encoding="utf-8")
    execution["status"] = summary["status"]
    execution["finished_at"] = utc_now()
    validate_execution_document(execution)
    write_json(run_dir / "execution.json", execution)
    return (0 if summary["status"] == "passed" else 1), run_dir


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(argv)
    try:
        exit_code, run_dir = run_benchmark(args)
    except (BenchmarkError, CommandError, ValueError, subprocess.TimeoutExpired) as error:
        print(f"idle benchmark 시작 실패: {error}", file=sys.stderr)
        return 2
    print(f"결과 폴더: {run_dir}", flush=True)
    return exit_code


if __name__ == "__main__":
    raise SystemExit(main())
