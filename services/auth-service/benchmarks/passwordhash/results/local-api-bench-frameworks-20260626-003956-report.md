# Auth Password Verify Framework Benchmark

- 실행 시각: 2026-06-26T00:46:51
- raw: `local-api-bench-frameworks-20260626-003956-raw.txt`
- jsonl: `local-api-bench-frameworks-20260626-003956.jsonl`
- csv: `local-api-bench-frameworks-20260626-003956.csv`
- 요청 수: 1000 per case
- 동시성: 16
- endpoint: `POST /bench/password/verify`
- hash: PBKDF2-HMAC-SHA256, 210000 iterations, 고정 fixture
- 범위: 로컬 HTTP API 경로 비교. DB, JWT, Kubernetes, Kong, HPA는 제외.

## 전체 요약

- process 계열 전체 최고 RPS: `go net/http workers=1` -> `177.996 RPS`
- process 계열 최저 p95: `go net/http workers=1` -> `155.232ms`
- python uvicorn 최고 RPS: workers=2 -> `50.553 RPS`, Python 최고 대비 `1.00x`
- go net/http 최고 RPS: workers=1 -> `177.996 RPS`, Python 최고 대비 `3.52x`
- nodejs node:http 최고 RPS: workers=4 -> `74.238 RPS`, Python 최고 대비 `1.47x`
- nodejs fastify 최고 RPS: workers=4 -> `77.496 RPS`, Python 최고 대비 `1.53x`
- rust tiny_http 최고 RPS: workers=2 -> `54.877 RPS`, Python 최고 대비 `1.09x`
- rust axum 최고 RPS: workers=4 -> `53.947 RPS`, Python 최고 대비 `1.07x`
- 모든 케이스 errors=0

### Process / Process-Fanout Results

| language | server | case | rps | mean | p50 | p95 | p99 | max | errors |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| python | uvicorn | workers=1 | 49.893 | 319.365ms | 304.674ms | 536.164ms | 658.481ms | 823.597ms | 0 |
| python | uvicorn | workers=2 | 50.553 | 315.441ms | 302.970ms | 449.328ms | 664.346ms | 858.611ms | 0 |
| python | uvicorn | workers=4 | 45.593 | 349.489ms | 330.264ms | 563.579ms | 699.575ms | 854.590ms | 0 |
| go | net/http | workers=1 | 177.996 | 89.288ms | 77.312ms | 155.232ms | 356.985ms | 450.023ms | 0 |
| go | net/http | workers=2 | 140.244 | 113.620ms | 97.013ms | 243.341ms | 401.002ms | 515.609ms | 0 |
| go | net/http | workers=4 | 145.193 | 109.863ms | 87.414ms | 271.495ms | 539.652ms | 704.694ms | 0 |
| nodejs | node:http | workers=1 | 38.154 | 416.636ms | 398.607ms | 556.578ms | 739.622ms | 791.913ms | 0 |
| nodejs | node:http | workers=2 | 63.224 | 250.910ms | 252.235ms | 383.083ms | 513.931ms | 666.612ms | 0 |
| nodejs | node:http | workers=4 | 74.238 | 214.232ms | 203.361ms | 367.446ms | 453.782ms | 536.655ms | 0 |
| nodejs | fastify | workers=1 | 41.864 | 380.065ms | 366.828ms | 481.204ms | 597.042ms | 656.293ms | 0 |
| nodejs | fastify | workers=2 | 66.463 | 238.995ms | 224.755ms | 412.234ms | 568.620ms | 668.035ms | 0 |
| nodejs | fastify | workers=4 | 77.496 | 205.407ms | 194.499ms | 358.661ms | 435.435ms | 521.778ms | 0 |
| rust | tiny_http | workers=1 | 52.759 | 302.489ms | 294.300ms | 450.237ms | 655.958ms | 730.870ms | 0 |
| rust | tiny_http | workers=2 | 54.877 | 290.737ms | 281.421ms | 438.169ms | 517.776ms | 552.204ms | 0 |
| rust | tiny_http | workers=4 | 52.378 | 305.061ms | 295.543ms | 453.921ms | 578.410ms | 656.568ms | 0 |
| rust | axum | workers=1 | 52.238 | 305.192ms | 295.656ms | 425.080ms | 542.434ms | 694.202ms | 0 |
| rust | axum | workers=2 | 50.845 | 313.581ms | 295.341ms | 481.483ms | 620.903ms | 734.320ms | 0 |
| rust | axum | workers=4 | 53.947 | 295.345ms | 273.031ms | 543.250ms | 642.062ms | 763.828ms | 0 |

### Go Single Process GOMAXPROCS Reference

| language | server | case | rps | mean | p50 | p95 | p99 | max | errors |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| go | net/http | GOMAXPROCS=1 | 33.153 | 480.342ms | 464.959ms | 597.708ms | 814.071ms | 926.981ms | 0 |
| go | net/http | GOMAXPROCS=2 | 65.275 | 243.876ms | 234.577ms | 372.922ms | 453.435ms | 543.864ms | 0 |
| go | net/http | GOMAXPROCS=4 | 124.955 | 127.207ms | 120.957ms | 197.766ms | 251.974ms | 330.564ms | 0 |

## 해석 메모

- 이번 run에서는 Go `net/http` workers=1이 process 계열 최고 RPS와 최저 p95를 동시에 기록했다. 이전 run에서는 Go workers=2/4가 더 좋았으므로 단일 실행 변동은 있다.
- Fastify는 node:http보다 소폭 개선됐다. 특히 workers=4 기준 node:http `74.238 RPS`, Fastify `77.496 RPS`로 근소하게 앞섰고 p95도 조금 낮았다.
- Axum은 tiny_http 대비 뚜렷한 개선을 보이지 않았다. 현재 Axum 구현은 PBKDF2를 `spawn_blocking`으로 넘기지만, CPU-bound KDF 비용 자체가 커서 HTTP framework 개선폭이 제한된 것으로 보인다.
- Rust 결과는 여전히 Go보다 낮다. 이는 Rust 언어 한계라기보다 구현 방식, blocking pool, HTTP stack, PBKDF2 crate/컴파일 옵션, 프로세스 fanout 방식 영향이 섞인 결과로 봐야 한다.
- Node는 libuv threadpool 기반 `crypto.pbkdf2`라 `UV_THREADPOOL_SIZE` 튜닝을 별도 축으로 볼 필요가 있다.
- 최종 판단 전에는 같은 조건 3회 반복과 median-of-runs를 추천한다.
