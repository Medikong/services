# Local API Runtime Benchmark

- 실행 시각: 2026-06-26T09:30:30
- raw: `local-api-bench-resource-fixed-smoke-20260626-092954-raw.txt`
- jsonl: `local-api-bench-resource-fixed-smoke-20260626-092954.jsonl`
- csv: `local-api-bench-resource-fixed-smoke-20260626-092954.csv`
- endpoint: `POST /bench/password/verify`
- hash: PBKDF2-HMAC-SHA256, 210000 iterations, 고정 fixture
- 범위: 로컬 HTTP API 경로 비교. DB, JWT, Kubernetes, Kong, HPA는 제외.

## fixed_cpu

- 최고 RPS: `rust axum workers=1` -> `41.726 RPS`
- 최저 p95: `rust axum workers=1` -> `23.864ms`
- errors 합계: `0`

### fixed_cpu process results

| case | cpu | rps | p50 | p95 | p99 | max | CPU avg/max | RSS avg/max | startup | size | samples | errors |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| python uvicorn workers=1 | control=not_strict, total=1, per=1, runtime=- | 18.902 | 52.580ms | 52.580ms | 52.580ms | 52.580ms | 49.300/49.300 | 86.250/86.250MB | 641.000ms | - | 1 | 0 |
| go net/http workers=1 | control=runtime_slots, total=1, per=1, runtime=1 | 37.342 | 26.633ms | 26.633ms | 26.633ms | 26.633ms | 2.500/2.500 | 10.312/10.312MB | 429.000ms | 8.010MB | 1 | 0 |
| nodejs node:http workers=1 | control=runtime_slots, total=1, per=1, runtime=1 | 17.604 | 56.519ms | 56.519ms | 56.519ms | 56.519ms | 15.700/15.700 | 50.891/50.891MB | 121.000ms | - | 1 | 0 |
| nodejs fastify workers=1 | control=runtime_slots, total=1, per=1, runtime=1 | 17.357 | 57.511ms | 57.511ms | 57.511ms | 57.511ms | 38.900/38.900 | 64.469/64.469MB | 210.000ms | 6.896MB | 1 | 0 |
| rust tiny_http workers=1 | control=not_strict, total=1, per=1, runtime=- | 40.786 | 24.418ms | 24.418ms | 24.418ms | 24.418ms | 0.700/0.700 | 1.844/1.844MB | 313.000ms | 0.987MB | 1 | 0 |
| rust axum workers=1 | control=runtime_slots, total=1, per=1, runtime=1 | 41.726 | 23.864ms | 23.864ms | 23.864ms | 23.864ms | 0.000/0.000 | 2.359/2.359MB | 309.000ms | 1.709MB | 1 | 0 |

### fixed_cpu scale-out efficiency

| language | server | total slots | case | rps | vs process=1 |
| --- | --- | ---: | --- | ---: | ---: |
| go | net/http | 1 | workers=1 | 37.342 | 1.00x |
| nodejs | fastify | 1 | workers=1 | 17.357 | 1.00x |
| nodejs | node:http | 1 | workers=1 | 17.604 | 1.00x |
| python | uvicorn | 1 | workers=1 | 18.902 | 1.00x |
| rust | axum | 1 | workers=1 | 41.726 | 1.00x |
| rust | tiny_http | 1 | workers=1 | 40.786 | 1.00x |

## 해석 메모

- `max_cpu`는 런타임이 가능한 병렬도를 쓰게 둔 로컬 최대 활용 관점이다.
- `fixed_cpu`는 runtime-level slot을 맞춰 process 분할 자체의 효율을 보는 관점이다.
- Python과 Rust `tiny_http`의 fixed CPU는 macOS 로컬에서 엄밀한 CPU 격리가 아니므로 `cpu_control=not_strict`로 표시한다.
- 리소스 지표는 macOS `ps` 기반 샘플링 근사값이다. CPU%는 순간 샘플 합산값이라 정밀한 CPU cycle 분석이 아니다.
- RSS는 하네스가 띄운 PID와 하위 프로세스의 샘플별 합산값으로 avg/max를 계산한다.
- 엄밀한 CPU 격리는 Docker/cpuset 기반 실험에서 별도로 확인해야 한다.
