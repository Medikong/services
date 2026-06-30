# Auth Password Verify 4-Language Local API Benchmark

- 실행 시각: 2026-06-26T00:03:53
- raw: `local-api-bench-4lang-20260625-235901-raw.txt`
- jsonl: `local-api-bench-4lang-20260625-235901.jsonl`
- csv: `local-api-bench-4lang-20260625-235901.csv`
- 요청 수: 1000 per case
- 동시성: 16
- endpoint: `POST /bench/password/verify`
- hash: PBKDF2-HMAC-SHA256, 210000 iterations, 고정 fixture
- 범위: 로컬 HTTP API 경로 비교. DB, JWT, Kubernetes, Kong, HPA는 제외.

## 전체 요약

- process 계열 전체 최고 RPS: `go net/http workers=4` -> `168.155 RPS`
- process 계열 최저 p95: `go net/http workers=2` -> `175.781ms`
- python 최고 RPS: workers=1 -> `51.823 RPS`, Python 최고 대비 `1.00x`
- go 최고 RPS: workers=4 -> `168.155 RPS`, Python 최고 대비 `3.24x`
- nodejs 최고 RPS: workers=2 -> `54.003 RPS`, Python 최고 대비 `1.04x`
- rust 최고 RPS: workers=4 -> `66.295 RPS`, Python 최고 대비 `1.28x`
- 모든 케이스 errors=0

### Process / Process-Fanout Results

| language | server | case | rps | mean | p50 | p95 | p99 | max | errors |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| python | uvicorn | workers=1 | 51.823 | 307.974ms | 271.024ms | 530.496ms | 643.950ms | 818.833ms | 0 |
| python | uvicorn | workers=2 | 50.539 | 315.539ms | 303.879ms | 469.769ms | 661.090ms | 798.366ms | 0 |
| python | uvicorn | workers=4 | 48.404 | 329.022ms | 309.441ms | 499.727ms | 792.810ms | 907.430ms | 0 |
| go | net/http | workers=1 | 122.137 | 130.366ms | 110.150ms | 267.003ms | 451.728ms | 813.699ms | 0 |
| go | net/http | workers=2 | 162.455 | 98.180ms | 91.272ms | 175.781ms | 337.608ms | 464.268ms | 0 |
| go | net/http | workers=4 | 168.155 | 94.732ms | 87.202ms | 189.259ms | 242.626ms | 349.935ms | 0 |
| nodejs | node:http | workers=1 | 39.939 | 398.023ms | 376.042ms | 547.495ms | 1022.890ms | 1173.223ms | 0 |
| nodejs | node:http | workers=2 | 54.003 | 293.701ms | 287.878ms | 572.234ms | 752.205ms | 827.326ms | 0 |
| nodejs | node:http | workers=4 | 49.160 | 324.156ms | 279.360ms | 637.338ms | 1105.050ms | 1793.428ms | 0 |
| rust | tiny_http | workers=1 | 53.916 | 295.930ms | 284.801ms | 457.046ms | 586.950ms | 652.757ms | 0 |
| rust | tiny_http | workers=2 | 62.191 | 256.672ms | 247.207ms | 341.971ms | 401.253ms | 3315.616ms | 0 |
| rust | tiny_http | workers=4 | 66.295 | 240.757ms | 240.668ms | 317.689ms | 390.131ms | 449.304ms | 0 |

### Go Single Process GOMAXPROCS Reference

| language | server | case | rps | mean | p50 | p95 | p99 | max | errors |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| go | net/http | GOMAXPROCS=1 | 38.170 | 417.606ms | 420.382ms | 550.963ms | 688.602ms | 1064.983ms | 0 |
| go | net/http | GOMAXPROCS=2 | 67.030 | 237.524ms | 222.806ms | 388.295ms | 565.843ms | 763.515ms | 0 |
| go | net/http | GOMAXPROCS=4 | 124.193 | 128.211ms | 121.989ms | 197.273ms | 232.172ms | 325.607ms | 0 |

## 해석 메모

- 이번 4언어 단일 run에서는 Go process-fanout이 가장 높은 처리량을 보였다. `workers=4`가 최고 RPS이고, `workers=2`가 p95 기준으로 더 안정적이다.
- Python은 이번 run에서 worker를 늘릴수록 RPS가 낮아졌다. 앞선 1000샘플 run과 방향이 달라 단일 실행 변동이 있다.
- Node.js는 async `crypto.pbkdf2`를 사용했지만, 이번 설정에서는 Python과 비슷하거나 낮은 처리량을 보였고 tail latency가 크게 튀었다. `UV_THREADPOOL_SIZE`가 기본 4라 별도 튜닝 여지가 있다.
- Rust 구현은 `tiny_http`에서 요청마다 thread를 생성하는 단순 구현이다. Rust 언어 자체의 한계라기보다 서버 구현 선택의 영향이 클 수 있다.
- Go `GOMAXPROCS=4`는 Python/Node/Rust process 계열보다 높지만, Go process-fanout 2/4보다는 낮았다. `GOMAXPROCS`와 process count는 별도 축이다.
- 최종 판단 전에는 같은 조건 3회 반복과 Node `UV_THREADPOOL_SIZE`, Rust 서버 구현(axum/hyper 등) 튜닝 실험을 분리해서 보는 편이 좋다.
