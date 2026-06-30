#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
AUTH_DIR="$(cd "$ROOT_DIR/../.." && pwd)"

REQUESTS="${REQUESTS:-40}"
CONCURRENCY="${CONCURRENCY:-4}"
WORKERS="${WORKERS:-1 2 4}"
MODE="${MODE:-max_cpu}"
TOTAL_CPU_SLOTS="${TOTAL_CPU_SLOTS:-1 2 4}"
GOMAXPROCS_VALUES="${GOMAXPROCS_VALUES:-1 2 4}"

PYTHON_PORT="${PYTHON_PORT:-18181}"
GO_PROCESS_BASE_PORT="${GO_PROCESS_BASE_PORT:-18280}"
GO_GOMAXPROCS_PORT="${GO_GOMAXPROCS_PORT:-18381}"
NODE_PROCESS_BASE_PORT="${NODE_PROCESS_BASE_PORT:-18480}"
NODE_FASTIFY_PROCESS_BASE_PORT="${NODE_FASTIFY_PROCESS_BASE_PORT:-18680}"
NODE_UV_THREADPOOL_SIZE="${NODE_UV_THREADPOOL_SIZE:-4}"
RUST_PROCESS_BASE_PORT="${RUST_PROCESS_BASE_PORT:-18580}"
RUST_AXUM_PROCESS_BASE_PORT="${RUST_AXUM_PROCESS_BASE_PORT:-18780}"
RESOURCE_SAMPLE_INTERVAL_SECONDS="${RESOURCE_SAMPLE_INTERVAL_SECONDS:-0.5}"
RESOURCE_SAMPLER="${RESOURCE_SAMPLER:-ps}"

PIDS=()
BIN_DIR="$(mktemp -d)"

GO_SERVER_BINARY_SIZE_MB=0
RUST_SERVER_BINARY_SIZE_MB=0
RUST_AXUM_SERVER_BINARY_SIZE_MB=0
NODE_FASTIFY_BUNDLE_SIZE_MB=0

stop_servers() {
  for pid in "${PIDS[@]:-}"; do
    if kill -0 "$pid" >/dev/null 2>&1; then
      kill "$pid" >/dev/null 2>&1 || true
      wait "$pid" >/dev/null 2>&1 || true
    fi
  done
  PIDS=()
}

cleanup() {
  stop_servers
  rm -rf "$BIN_DIR"
}
trap cleanup EXIT

wait_for_health() {
  local url="$1"
  python3 - "$url" <<'PY'
import sys
import time
import urllib.request

url = sys.argv[1]
deadline = time.monotonic() + 20
last_error = None
while time.monotonic() < deadline:
    try:
        with urllib.request.urlopen(url, timeout=1) as response:
            if response.status == 200:
                sys.exit(0)
    except Exception as exc:
        last_error = exc
    time.sleep(0.1)
raise SystemExit(f"server did not become healthy: {url}: {last_error}")
PY
}

now_ms() {
  python3 - <<'PY'
import time
print(int(time.time() * 1000))
PY
}

binary_size_mb() {
  local path="$1"
  if [[ ! -e "$path" ]]; then
    echo "0"
    return
  fi
  python3 - "$path" <<'PY'
import os
import sys
print(f"{os.path.getsize(sys.argv[1]) / 1024 / 1024:.3f}")
PY
}

directory_size_mb() {
  local path="$1"
  if [[ ! -e "$path" ]]; then
    echo "0"
    return
  fi
  python3 - "$path" <<'PY'
import os
import sys

total = 0
for root, _, files in os.walk(sys.argv[1]):
    for name in files:
        file_path = os.path.join(root, name)
        try:
            total += os.path.getsize(file_path)
        except OSError:
            pass
print(f"{total / 1024 / 1024:.3f}")
PY
}

pid_tree_csv() {
  local queue=("$@")
  local seen=""
  local result=()
  while [[ "${#queue[@]}" -gt 0 ]]; do
    local pid="${queue[0]}"
    queue=("${queue[@]:1}")
    [[ -n "$pid" ]] || continue
    if [[ " $seen " == *" $pid "* ]]; then
      continue
    fi
    seen+=" $pid"
    if kill -0 "$pid" >/dev/null 2>&1; then
      result+=("$pid")
      local children
      children="$(pgrep -P "$pid" 2>/dev/null || true)"
      if [[ -n "$children" ]]; then
        while IFS= read -r child; do
          [[ -n "$child" ]] && queue+=("$child")
        done <<<"$children"
      fi
    fi
  done
  local joined=""
  for pid in "${result[@]:-}"; do
    if [[ -n "$joined" ]]; then
      joined+=","
    fi
    joined+="$pid"
  done
  echo "$joined"
}

sample_resources_once() {
  local output_file="$1"
  shift
  local pid_csv
  pid_csv="$(pid_tree_csv "$@")"
  if [[ -z "$pid_csv" ]]; then
    return
  fi
  local ps_file="$BIN_DIR/resource-ps-${RANDOM}.txt"
  ps -o pcpu= -o rss= -p "$pid_csv" >"$ps_file" 2>/dev/null || true
  python3 - "$output_file" "$ps_file" <<'PY'
import sys
import time

output_path = sys.argv[1]
ps_path = sys.argv[2]
cpu_total = 0.0
rss_total_kb = 0
process_count = 0
with open(ps_path, encoding="utf-8") as file:
    for line in file:
        parts = line.split()
        if len(parts) < 2:
            continue
        try:
            cpu_total += float(parts[0])
            rss_total_kb += int(float(parts[1]))
            process_count += 1
        except ValueError:
            continue

if process_count:
    with open(output_path, "a", encoding="utf-8") as file:
        file.write(f"{time.time():.6f}\t{cpu_total:.3f}\t{rss_total_kb}\t{process_count}\n")
PY
  rm -f "$ps_file"
}

start_resource_sampler() {
  local output_file="$1"
  local stop_file="$2"
  shift 2
  : >"$output_file"
  while [[ ! -f "$stop_file" ]]; do
    sample_resources_once "$output_file" "$@"
    sleep "$RESOURCE_SAMPLE_INTERVAL_SECONDS"
  done
}

merge_bench_result() {
  local result_file="$1"
  local sample_file="$2"
  local startup_time_ms="$3"
  local binary_size_mb="$4"
  python3 - "$result_file" "$sample_file" "$RESOURCE_SAMPLER" "$startup_time_ms" "$binary_size_mb" <<'PY'
import json
import sys

result_path, sample_path, sampler, startup_time_ms, binary_size_mb = sys.argv[1:]
with open(result_path, encoding="utf-8") as file:
    result = json.load(file)

samples = []
try:
    with open(sample_path, encoding="utf-8") as file:
        for line in file:
            parts = line.split()
            if len(parts) != 4:
                continue
            samples.append(
                {
                    "cpu": float(parts[1]),
                    "rss_mb": int(parts[2]) / 1024,
                    "processes": int(parts[3]),
                }
            )
except FileNotFoundError:
    samples = []

if samples:
    result["server_cpu_percent_avg"] = round(sum(item["cpu"] for item in samples) / len(samples), 3)
    result["server_cpu_percent_max"] = round(max(item["cpu"] for item in samples), 3)
    result["server_rss_mb_avg"] = round(sum(item["rss_mb"] for item in samples) / len(samples), 3)
    result["server_rss_mb_max"] = round(max(item["rss_mb"] for item in samples), 3)
    result["server_process_count_max"] = max(item["processes"] for item in samples)
else:
    result["server_cpu_percent_avg"] = None
    result["server_cpu_percent_max"] = None
    result["server_rss_mb_avg"] = None
    result["server_rss_mb_max"] = None
    result["server_process_count_max"] = None

result["resource_sample_count"] = len(samples)
result["resource_sampler"] = sampler
result["startup_time_ms"] = round(float(startup_time_ms), 3)
size = float(binary_size_mb)
result["binary_size_mb"] = round(size, 3) if size > 0 else None

print(json.dumps(result, indent=2))
PY
}

benchclient() {
  local targets="$1"
  local language="$2"
  local server="$3"
  local worker_model="$4"
  local workers="$5"
  local mode="$6"
  local total_slots="$7"
  local per_process_slots="$8"
  local runtime_slots="$9"
  local cpu_control="${10}"
  local gomaxprocs="${11:-0}"
  local startup_time_ms="${12:-0}"
  local binary_size_mb="${13:-0}"

  local args=(
    -targets "$targets"
    -requests "$REQUESTS"
    -concurrency "$CONCURRENCY"
    -language "$language"
    -server "$server"
    -worker-model "$worker_model"
    -workers "$workers"
    -mode "$mode"
    -cpu-control "$cpu_control"
  )
  if [[ "$total_slots" -gt 0 ]]; then
    args+=(-total-cpu-slots "$total_slots")
  fi
  if [[ "$per_process_slots" -gt 0 ]]; then
    args+=(-per-process-cpu-slots "$per_process_slots")
  fi
  if [[ "$runtime_slots" -gt 0 ]]; then
    args+=(-runtime-slots "$runtime_slots")
  fi
  if [[ "$gomaxprocs" -gt 0 ]]; then
    args+=(-gomaxprocs "$gomaxprocs")
  fi
  local result_file="$BIN_DIR/bench-result-${language}-${server//\//_}-${worker_model}-${mode}-${workers}-${RANDOM}.json"
  local sample_file="$BIN_DIR/resource-samples-${language}-${server//\//_}-${worker_model}-${mode}-${workers}-${RANDOM}.tsv"
  local stop_file="$BIN_DIR/resource-stop-${RANDOM}"
  start_resource_sampler "$sample_file" "$stop_file" "${PIDS[@]}" &
  local sampler_pid="$!"
  "$BIN_DIR/benchclient" "${args[@]}" >"$result_file"
  touch "$stop_file"
  wait "$sampler_pid" >/dev/null 2>&1 || true
  merge_bench_result "$result_file" "$sample_file" "$startup_time_ms" "$binary_size_mb"
}

run_python_workers() {
  local workers="$1"
  local mode="$2"
  local total_slots="$3"
  local per_process_slots="$4"
  local cpu_control="max"
  if [[ "$mode" == "fixed_cpu" ]]; then
    cpu_control="not_strict"
  fi

  local started_ms
  started_ms="$(now_ms)"
  (
    cd "$AUTH_DIR"
    uv run python -m uvicorn app.password_bench_api:app \
      --host 127.0.0.1 \
      --port "$PYTHON_PORT" \
      --workers "$workers" \
      --log-level warning
  ) >"$BIN_DIR/python-workers-${workers}-${mode}.log" 2>&1 &
  PIDS+=("$!")
  wait_for_health "http://127.0.0.1:${PYTHON_PORT}/health"
  local startup_time_ms=$(( $(now_ms) - started_ms ))
  benchclient "http://127.0.0.1:${PYTHON_PORT}" python uvicorn process "$workers" "$mode" "$total_slots" "$per_process_slots" 0 "$cpu_control" 0 "$startup_time_ms" 0
  stop_servers
}

run_go_processes() {
  local workers="$1"
  local mode="$2"
  local total_slots="$3"
  local per_process_slots="$4"
  local runtime_slots=0
  local cpu_control="max"
  local gomaxprocs=0
  if [[ "$mode" == "fixed_cpu" ]]; then
    runtime_slots="$per_process_slots"
    cpu_control="runtime_slots"
    gomaxprocs="$per_process_slots"
  fi

  local targets=""
  local started_ms
  started_ms="$(now_ms)"
  for index in $(seq 1 "$workers"); do
    local port=$((GO_PROCESS_BASE_PORT + index))
    if [[ "$mode" == "fixed_cpu" ]]; then
      GOMAXPROCS="$per_process_slots" "$BIN_DIR/server" -addr "127.0.0.1:${port}" >"$BIN_DIR/go-process-${index}-${mode}.log" 2>&1 &
    else
      "$BIN_DIR/server" -addr "127.0.0.1:${port}" >"$BIN_DIR/go-process-${index}-${mode}.log" 2>&1 &
    fi
    PIDS+=("$!")
    wait_for_health "http://127.0.0.1:${port}/health"
    if [[ -n "$targets" ]]; then
      targets+=","
    fi
    targets+="http://127.0.0.1:${port}"
  done
  local startup_time_ms=$(( $(now_ms) - started_ms ))
  benchclient "$targets" go net/http process-fanout "$workers" "$mode" "$total_slots" "$per_process_slots" "$runtime_slots" "$cpu_control" "$gomaxprocs" "$startup_time_ms" "$GO_SERVER_BINARY_SIZE_MB"
  stop_servers
}

run_node_processes() {
  local workers="$1"
  local mode="$2"
  local total_slots="$3"
  local per_process_slots="$4"
  local runtime_slots="$NODE_UV_THREADPOOL_SIZE"
  local cpu_control="max"
  if [[ "$mode" == "fixed_cpu" ]]; then
    runtime_slots="$per_process_slots"
    cpu_control="runtime_slots"
  fi

  local targets=""
  local started_ms
  started_ms="$(now_ms)"
  for index in $(seq 1 "$workers"); do
    local port=$((NODE_PROCESS_BASE_PORT + index))
    UV_THREADPOOL_SIZE="$runtime_slots" node "$ROOT_DIR/node/server.mjs" --addr "127.0.0.1:${port}" >"$BIN_DIR/node-process-${index}-${mode}.log" 2>&1 &
    PIDS+=("$!")
    wait_for_health "http://127.0.0.1:${port}/health"
    if [[ -n "$targets" ]]; then
      targets+=","
    fi
    targets+="http://127.0.0.1:${port}"
  done
  local startup_time_ms=$(( $(now_ms) - started_ms ))
  benchclient "$targets" nodejs node:http process-fanout "$workers" "$mode" "$total_slots" "$per_process_slots" "$runtime_slots" "$cpu_control" 0 "$startup_time_ms" 0
  stop_servers
}

run_node_fastify_processes() {
  local workers="$1"
  local mode="$2"
  local total_slots="$3"
  local per_process_slots="$4"
  local runtime_slots="$NODE_UV_THREADPOOL_SIZE"
  local cpu_control="max"
  if [[ "$mode" == "fixed_cpu" ]]; then
    runtime_slots="$per_process_slots"
    cpu_control="runtime_slots"
  fi

  local targets=""
  local started_ms
  started_ms="$(now_ms)"
  for index in $(seq 1 "$workers"); do
    local port=$((NODE_FASTIFY_PROCESS_BASE_PORT + index))
    UV_THREADPOOL_SIZE="$runtime_slots" node "$BIN_DIR/node-fastify/server.mjs" --addr "127.0.0.1:${port}" >"$BIN_DIR/node-fastify-process-${index}-${mode}.log" 2>&1 &
    PIDS+=("$!")
    wait_for_health "http://127.0.0.1:${port}/health"
    if [[ -n "$targets" ]]; then
      targets+=","
    fi
    targets+="http://127.0.0.1:${port}"
  done
  local startup_time_ms=$(( $(now_ms) - started_ms ))
  benchclient "$targets" nodejs fastify process-fanout "$workers" "$mode" "$total_slots" "$per_process_slots" "$runtime_slots" "$cpu_control" 0 "$startup_time_ms" "$NODE_FASTIFY_BUNDLE_SIZE_MB"
  stop_servers
}

run_rust_processes() {
  local workers="$1"
  local mode="$2"
  local total_slots="$3"
  local per_process_slots="$4"
  local cpu_control="max"
  if [[ "$mode" == "fixed_cpu" ]]; then
    cpu_control="not_strict"
  fi

  local targets=""
  local started_ms
  started_ms="$(now_ms)"
  for index in $(seq 1 "$workers"); do
    local port=$((RUST_PROCESS_BASE_PORT + index))
    "$BIN_DIR/rust-server" --addr "127.0.0.1:${port}" >"$BIN_DIR/rust-process-${index}-${mode}.log" 2>&1 &
    PIDS+=("$!")
    wait_for_health "http://127.0.0.1:${port}/health"
    if [[ -n "$targets" ]]; then
      targets+=","
    fi
    targets+="http://127.0.0.1:${port}"
  done
  local startup_time_ms=$(( $(now_ms) - started_ms ))
  benchclient "$targets" rust tiny_http process-fanout "$workers" "$mode" "$total_slots" "$per_process_slots" 0 "$cpu_control" 0 "$startup_time_ms" "$RUST_SERVER_BINARY_SIZE_MB"
  stop_servers
}

run_rust_axum_processes() {
  local workers="$1"
  local mode="$2"
  local total_slots="$3"
  local per_process_slots="$4"
  local runtime_slots=0
  local cpu_control="max"
  if [[ "$mode" == "fixed_cpu" ]]; then
    runtime_slots="$per_process_slots"
    cpu_control="runtime_slots"
  fi

  local targets=""
  local started_ms
  started_ms="$(now_ms)"
  for index in $(seq 1 "$workers"); do
    local port=$((RUST_AXUM_PROCESS_BASE_PORT + index))
    if [[ "$mode" == "fixed_cpu" ]]; then
      "$BIN_DIR/rust-axum-server" \
        --addr "127.0.0.1:${port}" \
        --worker-threads "$per_process_slots" \
        --max-blocking-threads "$per_process_slots" \
        >"$BIN_DIR/rust-axum-process-${index}-${mode}.log" 2>&1 &
    else
      "$BIN_DIR/rust-axum-server" --addr "127.0.0.1:${port}" >"$BIN_DIR/rust-axum-process-${index}-${mode}.log" 2>&1 &
    fi
    PIDS+=("$!")
    wait_for_health "http://127.0.0.1:${port}/health"
    if [[ -n "$targets" ]]; then
      targets+=","
    fi
    targets+="http://127.0.0.1:${port}"
  done
  local startup_time_ms=$(( $(now_ms) - started_ms ))
  benchclient "$targets" rust axum process-fanout "$workers" "$mode" "$total_slots" "$per_process_slots" "$runtime_slots" "$cpu_control" 0 "$startup_time_ms" "$RUST_AXUM_SERVER_BINARY_SIZE_MB"
  stop_servers
}

run_go_gomaxprocs() {
  local gomaxprocs="$1"
  local started_ms
  started_ms="$(now_ms)"
  GOMAXPROCS="$gomaxprocs" "$BIN_DIR/server" -addr "127.0.0.1:${GO_GOMAXPROCS_PORT}" >"$BIN_DIR/go-gomaxprocs-${gomaxprocs}.log" 2>&1 &
  PIDS+=("$!")
  wait_for_health "http://127.0.0.1:${GO_GOMAXPROCS_PORT}/health"
  local startup_time_ms=$(( $(now_ms) - started_ms ))
  benchclient "http://127.0.0.1:${GO_GOMAXPROCS_PORT}" go net/http gomaxprocs 1 max_cpu 0 0 "$gomaxprocs" runtime_slots "$gomaxprocs" "$startup_time_ms" "$GO_SERVER_BINARY_SIZE_MB"
  stop_servers
}

prepare_binaries() {
  go -C "$ROOT_DIR" build -o "$BIN_DIR/server" ./cmd/server
  go -C "$ROOT_DIR" build -o "$BIN_DIR/benchclient" ./cmd/benchclient
  GO_SERVER_BINARY_SIZE_MB="$(binary_size_mb "$BIN_DIR/server")"

  mkdir -p "$BIN_DIR/node-fastify"
  cp "$ROOT_DIR/node-fastify/package.json" "$BIN_DIR/node-fastify/package.json"
  if [[ -f "$ROOT_DIR/node-fastify/package-lock.json" ]]; then
    cp "$ROOT_DIR/node-fastify/package-lock.json" "$BIN_DIR/node-fastify/package-lock.json"
  fi
  cp "$ROOT_DIR/node-fastify/server.mjs" "$BIN_DIR/node-fastify/server.mjs"
  if ! npm --prefix "$BIN_DIR/node-fastify" install --no-audit --no-fund >"$BIN_DIR/node-fastify-install.log" 2>&1; then
    cat "$BIN_DIR/node-fastify-install.log" >&2
    exit 1
  fi
  NODE_FASTIFY_BUNDLE_SIZE_MB="$(directory_size_mb "$BIN_DIR/node-fastify")"

  if ! CARGO_TARGET_DIR="$BIN_DIR/rust-target" cargo build --manifest-path "$ROOT_DIR/rust-server/Cargo.toml" --release >"$BIN_DIR/rust-build.log" 2>&1; then
    cat "$BIN_DIR/rust-build.log" >&2
    exit 1
  fi
  cp "$BIN_DIR/rust-target/release/medikong-auth-passwordhash-rust-server" "$BIN_DIR/rust-server"
  RUST_SERVER_BINARY_SIZE_MB="$(binary_size_mb "$BIN_DIR/rust-server")"

  if ! CARGO_TARGET_DIR="$BIN_DIR/rust-axum-target" cargo build --manifest-path "$ROOT_DIR/rust-axum-server/Cargo.toml" --release >"$BIN_DIR/rust-axum-build.log" 2>&1; then
    cat "$BIN_DIR/rust-axum-build.log" >&2
    exit 1
  fi
  cp "$BIN_DIR/rust-axum-target/release/medikong-auth-passwordhash-axum-server" "$BIN_DIR/rust-axum-server"
  RUST_AXUM_SERVER_BINARY_SIZE_MB="$(binary_size_mb "$BIN_DIR/rust-axum-server")"
}

run_suite_for_case() {
  local mode="$1"
  local workers="$2"
  local total_slots="$3"
  local per_process_slots="$4"
  run_python_workers "$workers" "$mode" "$total_slots" "$per_process_slots"
  run_go_processes "$workers" "$mode" "$total_slots" "$per_process_slots"
  run_node_processes "$workers" "$mode" "$total_slots" "$per_process_slots"
  run_node_fastify_processes "$workers" "$mode" "$total_slots" "$per_process_slots"
  run_rust_processes "$workers" "$mode" "$total_slots" "$per_process_slots"
  run_rust_axum_processes "$workers" "$mode" "$total_slots" "$per_process_slots"
}

run_max_cpu() {
  for workers in $WORKERS; do
    run_suite_for_case max_cpu "$workers" 0 0
  done
  for gomaxprocs in $GOMAXPROCS_VALUES; do
    run_go_gomaxprocs "$gomaxprocs"
  done
}

run_fixed_cpu() {
  for total_slots in $TOTAL_CPU_SLOTS; do
    for workers in $WORKERS; do
      if [[ "$workers" -le "$total_slots" && $((total_slots % workers)) -eq 0 ]]; then
        local per_process_slots=$((total_slots / workers))
        run_suite_for_case fixed_cpu "$workers" "$total_slots" "$per_process_slots"
      fi
    done
  done
}

prepare_binaries
case "$MODE" in
  max_cpu)
    run_max_cpu
    ;;
  fixed_cpu)
    run_fixed_cpu
    ;;
  all)
    run_max_cpu
    run_fixed_cpu
    ;;
  *)
    echo "MODE must be max_cpu, fixed_cpu, or all: $MODE" >&2
    exit 1
    ;;
esac
