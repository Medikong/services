#!/usr/bin/env python3
from __future__ import annotations

import csv
import json
import re
import sys
from collections import defaultdict
from datetime import datetime
from pathlib import Path
from typing import Any


FIELDS = [
    "mode",
    "language",
    "server",
    "worker_model",
    "workers",
    "processes",
    "cpu_control",
    "total_cpu_slots",
    "per_process_cpu_slots",
    "runtime_slots",
    "gomaxprocs",
    "requests",
    "concurrency",
    "throughput_rps",
    "mean_ms",
    "min_ms",
    "p50_ms",
    "p95_ms",
    "p99_ms",
    "max_ms",
    "errors",
    "elapsed_ms",
    "startup_time_ms",
    "binary_size_mb",
    "server_cpu_percent_avg",
    "server_cpu_percent_max",
    "server_rss_mb_avg",
    "server_rss_mb_max",
    "server_process_count_max",
    "resource_sample_count",
    "resource_sampler",
]


def main() -> None:
    if len(sys.argv) != 2:
        raise SystemExit("usage: write-local-api-bench-report.py <raw-output.txt>")

    raw_path = Path(sys.argv[1])
    rows = parse_json_objects(raw_path.read_text())
    if not rows:
        raise SystemExit(f"no benchmark JSON objects found in {raw_path}")

    stem = str(raw_path.with_suffix("")).replace("-raw", "")
    jsonl_path = Path(stem + ".jsonl")
    csv_path = Path(stem + ".csv")
    report_path = Path(stem + "-report.md")

    jsonl_path.write_text("".join(json.dumps(row, sort_keys=True) + "\n" for row in rows))
    write_csv(csv_path, rows)
    report_path.write_text(render_report(raw_path, jsonl_path, csv_path, rows))

    print(
        json.dumps(
            {
                "raw": str(raw_path),
                "jsonl": str(jsonl_path),
                "csv": str(csv_path),
                "report": str(report_path),
            },
            indent=2,
        )
    )


def parse_json_objects(text: str) -> list[dict[str, Any]]:
    return [json.loads(match.group(0)) for match in re.finditer(r"\{[^{}]*(?:\n[^{}]*)*\}", text)]


def write_csv(path: Path, rows: list[dict[str, Any]]) -> None:
    with path.open("w", newline="") as file:
        writer = csv.DictWriter(file, fieldnames=FIELDS, extrasaction="ignore")
        writer.writeheader()
        writer.writerows(rows)


def render_report(raw_path: Path, jsonl_path: Path, csv_path: Path, rows: list[dict[str, Any]]) -> str:
    grouped_by_mode: dict[str, list[dict[str, Any]]] = defaultdict(list)
    for row in rows:
        grouped_by_mode[row.get("mode", "unknown")].append(row)

    parts = [
        "# Local API Runtime Benchmark",
        "",
        f"- 실행 시각: {datetime.now().isoformat(timespec='seconds')}",
        f"- raw: `{raw_path.name}`",
        f"- jsonl: `{jsonl_path.name}`",
        f"- csv: `{csv_path.name}`",
        "- endpoint: `POST /bench/password/verify`",
        "- hash: PBKDF2-HMAC-SHA256, 210000 iterations, 고정 fixture",
        "- 범위: 로컬 HTTP API 경로 비교. DB, JWT, Kubernetes, Kong, HPA는 제외.",
        "",
    ]

    for mode in sorted(grouped_by_mode):
        mode_rows = grouped_by_mode[mode]
        process_rows = [row for row in mode_rows if row.get("worker_model") != "gomaxprocs"]
        gomax_rows = [row for row in mode_rows if row.get("worker_model") == "gomaxprocs"]
        parts.append(render_mode_summary(mode, process_rows))
        parts.append(render_table(process_rows, f"{mode} process results"))
        if gomax_rows:
            parts.append(render_table(gomax_rows, f"{mode} Go GOMAXPROCS reference"))
        if mode == "fixed_cpu":
            parts.append(render_scale_out_efficiency(process_rows))

    parts.append("## 해석 메모")
    parts.append("")
    parts.append("- `max_cpu`는 런타임이 가능한 병렬도를 쓰게 둔 로컬 최대 활용 관점이다.")
    parts.append("- `fixed_cpu`는 runtime-level slot을 맞춰 process 분할 자체의 효율을 보는 관점이다.")
    parts.append("- Python과 Rust `tiny_http`의 fixed CPU는 macOS 로컬에서 엄밀한 CPU 격리가 아니므로 `cpu_control=not_strict`로 표시한다.")
    parts.append("- 리소스 지표는 macOS `ps` 기반 샘플링 근사값이다. CPU%는 순간 샘플 합산값이라 정밀한 CPU cycle 분석이 아니다.")
    parts.append("- RSS는 하네스가 띄운 PID와 하위 프로세스의 샘플별 합산값으로 avg/max를 계산한다.")
    parts.append("- 엄밀한 CPU 격리는 Docker/cpuset 기반 실험에서 별도로 확인해야 한다.")
    return "\n".join(parts) + "\n"


def render_mode_summary(mode: str, rows: list[dict[str, Any]]) -> str:
    if not rows:
        return f"## {mode}\n\n- process 결과 없음\n"
    best_rps = max(rows, key=lambda row: row["throughput_rps"])
    best_p95 = min(rows, key=lambda row: row["p95_ms"])
    lines = [
        f"## {mode}",
        "",
        f"- 최고 RPS: `{label(best_rps)}` -> `{best_rps['throughput_rps']:.3f} RPS`",
        f"- 최저 p95: `{label(best_p95)}` -> `{best_p95['p95_ms']:.3f}ms`",
        f"- errors 합계: `{sum(int(row.get('errors', 0)) for row in rows)}`",
        "",
    ]
    return "\n".join(lines)


def render_table(rows: list[dict[str, Any]], title: str) -> str:
    if not rows:
        return ""
    lines = [
        f"### {title}",
        "",
        "| case | cpu | rps | p50 | p95 | p99 | max | CPU avg/max | RSS avg/max | startup | size | samples | errors |",
        "| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |",
    ]
    for row in sorted(rows, key=sort_key):
        cpu = cpu_label(row)
        lines.append(
            f"| {label(row)} | {cpu} | {row['throughput_rps']:.3f} | "
            f"{row['p50_ms']:.3f}ms | {row['p95_ms']:.3f}ms | {row['p99_ms']:.3f}ms | "
            f"{row['max_ms']:.3f}ms | {resource_pair(row, 'server_cpu_percent_avg', 'server_cpu_percent_max', '')} | "
            f"{resource_pair(row, 'server_rss_mb_avg', 'server_rss_mb_max', 'MB')} | "
            f"{metric(row, 'startup_time_ms', 'ms')} | {metric(row, 'binary_size_mb', 'MB')} | "
            f"{value(row.get('resource_sample_count'))} | {row['errors']} |"
        )
    lines.append("")
    return "\n".join(lines)


def render_scale_out_efficiency(rows: list[dict[str, Any]]) -> str:
    by_group: dict[tuple[Any, ...], list[dict[str, Any]]] = defaultdict(list)
    for row in rows:
        if row.get("total_cpu_slots") is None:
            continue
        key = (row["language"], row["server"], row["total_cpu_slots"])
        by_group[key].append(row)

    lines = [
        "### fixed_cpu scale-out efficiency",
        "",
        "| language | server | total slots | case | rps | vs process=1 |",
        "| --- | --- | ---: | --- | ---: | ---: |",
    ]
    for (language, server, total_slots), group_rows in sorted(by_group.items()):
        baseline = next((row for row in group_rows if row["workers"] == 1), None)
        if baseline is None:
            continue
        for row in sorted(group_rows, key=lambda item: item["workers"]):
            ratio = row["throughput_rps"] / baseline["throughput_rps"]
            lines.append(
                f"| {language} | {server} | {total_slots} | workers={row['workers']} | "
                f"{row['throughput_rps']:.3f} | {ratio:.2f}x |"
            )
    lines.append("")
    return "\n".join(lines)


def label(row: dict[str, Any]) -> str:
    case = f"GOMAXPROCS={row['gomaxprocs']}" if row.get("worker_model") == "gomaxprocs" else f"workers={row['workers']}"
    return f"{row['language']} {row['server']} {case}"


def cpu_label(row: dict[str, Any]) -> str:
    return (
        f"control={row.get('cpu_control', 'unknown')}, "
        f"total={value(row.get('total_cpu_slots'))}, "
        f"per={value(row.get('per_process_cpu_slots'))}, "
        f"runtime={value(row.get('runtime_slots'))}"
    )


def value(item: Any) -> str:
    return "-" if item is None else str(item)


def metric(row: dict[str, Any], key: str, suffix: str) -> str:
    item = row.get(key)
    if item is None:
        return "-"
    return f"{float(item):.3f}{suffix}"


def resource_pair(row: dict[str, Any], avg_key: str, max_key: str, suffix: str) -> str:
    avg = row.get(avg_key)
    max_value = row.get(max_key)
    if avg is None or max_value is None:
        return "-"
    return f"{float(avg):.3f}/{float(max_value):.3f}{suffix}"


def sort_key(row: dict[str, Any]) -> tuple[Any, ...]:
    order = {
        ("python", "uvicorn"): 0,
        ("go", "net/http"): 1,
        ("nodejs", "node:http"): 2,
        ("nodejs", "fastify"): 3,
        ("rust", "tiny_http"): 4,
        ("rust", "axum"): 5,
    }
    return (
        order.get((row.get("language"), row.get("server")), 99),
        row.get("total_cpu_slots") or 0,
        row.get("workers") or 0,
        row.get("gomaxprocs") or 0,
    )


if __name__ == "__main__":
    main()
