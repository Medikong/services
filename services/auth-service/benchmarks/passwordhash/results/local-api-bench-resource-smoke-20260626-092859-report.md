# Local API Runtime Benchmark

- 실행 시각: 2026-06-26T09:29:45
- raw: `local-api-bench-resource-smoke-20260626-092859-raw.txt`
- jsonl: `local-api-bench-resource-smoke-20260626-092859.jsonl`
- csv: `local-api-bench-resource-smoke-20260626-092859.csv`
- endpoint: `POST /bench/password/verify`
- hash: PBKDF2-HMAC-SHA256, 210000 iterations, 고정 fixture
- 범위: 로컬 HTTP API 경로 비교. DB, JWT, Kubernetes, Kong, HPA는 제외.

## max_cpu

- 최고 RPS: `rust axum workers=1` -> `42.622 RPS`
- 최저 p95: `rust axum workers=1` -> `23.354ms`
- errors 합계: `0`

### max_cpu process results

| case | cpu | rps | p50 | p95 | p99 | max | CPU avg/max | RSS avg/max | startup | size | samples | errors |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| python uvicorn workers=1 | control=max, total=-, per=-, runtime=- | 19.199 | 51.584ms | 51.584ms | 51.584ms | 51.584ms | 36.200/36.200 | 86.594/86.594MB | 779.000ms | - | 1 | 0 |
| go net/http workers=1 | control=max, total=-, per=-, runtime=- | 39.020 | 25.456ms | 25.456ms | 25.456ms | 25.456ms | 2.700/2.700 | 10.734/10.734MB | 637.000ms | 8.010MB | 1 | 0 |
| nodejs node:http workers=1 | control=max, total=-, per=-, runtime=4 | 18.299 | 54.553ms | 54.553ms | 54.553ms | 54.553ms | 14.100/14.100 | 51.344/51.344MB | 100.000ms | - | 1 | 0 |
| nodejs fastify workers=1 | control=max, total=-, per=-, runtime=4 | 17.734 | 56.289ms | 56.289ms | 56.289ms | 56.289ms | 32.400/32.400 | 64.688/64.688MB | 207.000ms | 6.896MB | 1 | 0 |
| rust tiny_http workers=1 | control=max, total=-, per=-, runtime=- | 40.678 | 24.477ms | 24.477ms | 24.477ms | 24.477ms | 0.000/0.000 | 1.859/1.859MB | 480.000ms | 0.987MB | 1 | 0 |
| rust axum workers=1 | control=max, total=-, per=-, runtime=- | 42.622 | 23.354ms | 23.354ms | 23.354ms | 23.354ms | 0.800/0.800 | 2.609/2.609MB | 513.000ms | 1.709MB | 1 | 0 |

### max_cpu Go GOMAXPROCS reference

| case | cpu | rps | p50 | p95 | p99 | max | CPU avg/max | RSS avg/max | startup | size | samples | errors |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| go net/http GOMAXPROCS=1 | control=runtime_slots, total=-, per=-, runtime=1 | 36.752 | 27.075ms | 27.075ms | 27.075ms | 27.075ms | 1.300/1.300 | 10.438/10.438MB | 102.000ms | 8.010MB | 1 | 0 |

## 해석 메모

- `max_cpu`는 런타임이 가능한 병렬도를 쓰게 둔 로컬 최대 활용 관점이다.
- `fixed_cpu`는 runtime-level slot을 맞춰 process 분할 자체의 효율을 보는 관점이다.
- Python과 Rust `tiny_http`의 fixed CPU는 macOS 로컬에서 엄밀한 CPU 격리가 아니므로 `cpu_control=not_strict`로 표시한다.
- 리소스 지표는 macOS `ps` 기반 샘플링 근사값이다. CPU%는 순간 샘플 합산값이라 정밀한 CPU cycle 분석이 아니다.
- RSS는 하네스가 띄운 PID와 하위 프로세스의 샘플별 합산값으로 avg/max를 계산한다.
- 엄밀한 CPU 격리는 Docker/cpuset 기반 실험에서 별도로 확인해야 한다.
