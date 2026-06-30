# Auth Password Verify Local API Benchmark

- 실행 시각: 2026-06-25T23:39:58
- raw: `local-api-bench-20260625-233736-raw.txt`
- jsonl: `local-api-bench-20260625-233736.jsonl`
- csv: `local-api-bench-20260625-233736.csv`
- 요청 수: 1000 per case
- 동시성: 16
- endpoint: `POST /bench/password/verify`
- hash: PBKDF2-HMAC-SHA256, 210000 iterations, 고정 fixture
- 범위: 로컬 HTTP API 경로 비교. DB, JWT, Kubernetes, Kong, HPA는 제외.

## 전체 요약

- 전체 최고 RPS: `go process-fanout workers=4` -> `219.350 RPS`
- 전체 최저 p95: `go process-fanout workers=1` -> `114.672ms`
- Python 최고 RPS: workers=4 -> `66.406 RPS`
- Python 최저 p95: workers=2 -> `338.368ms`
- Go process-fanout 최고 RPS: workers=4 -> `219.350 RPS`, Python 최고 대비 `3.30x`
- Go process-fanout 최저 p95: workers=1 -> `114.672ms`
- Go GOMAXPROCS 최고 RPS: GOMAXPROCS=4 -> `135.416 RPS`, Python 최고 대비 `2.04x`
- 모든 케이스 errors=0

### Python Uvicorn process workers

| case | rps | mean | p50 | p95 | p99 | max | errors |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| workers=1 | 57.635 | 276.935ms | 265.883ms | 403.155ms | 548.401ms | 737.228ms | 0 |
| workers=2 | 62.688 | 254.525ms | 251.078ms | 338.368ms | 429.451ms | 511.670ms | 0 |
| workers=4 | 66.406 | 240.020ms | 232.544ms | 342.087ms | 450.486ms | 594.906ms | 0 |

### Go net/http process fanout

| case | rps | mean | p50 | p95 | p99 | max | errors |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| workers=1 | 213.310 | 74.626ms | 71.485ms | 114.672ms | 142.881ms | 217.231ms | 0 |
| workers=2 | 200.536 | 79.387ms | 73.091ms | 143.277ms | 273.103ms | 405.593ms | 0 |
| workers=4 | 219.350 | 72.654ms | 65.734ms | 143.274ms | 180.442ms | 260.137ms | 0 |

### Go net/http single process GOMAXPROCS

| case | rps | mean | p50 | p95 | p99 | max | errors |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| GOMAXPROCS=1 | 36.208 | 440.173ms | 428.031ms | 587.269ms | 772.095ms | 1486.872ms | 0 |
| GOMAXPROCS=2 | 70.535 | 226.031ms | 217.126ms | 333.059ms | 529.114ms | 759.672ms | 0 |
| GOMAXPROCS=4 | 135.416 | 117.676ms | 113.104ms | 175.293ms | 216.277ms | 315.859ms | 0 |

## 해석 메모

- 1000샘플에서는 Python Uvicorn workers가 1 -> 2 -> 4로 갈수록 RPS가 조금씩 좋아졌다. 다만 p95는 workers=2가 가장 낮고, workers=4는 RPS 최고지만 p95/p99가 workers=2보다 높다.
- Go process-fanout은 workers=4가 최고 RPS이고, workers=1이 최저 p95다. workers=2는 이번 run에서 p99/max가 많이 튀었다.
- Go 단일 프로세스 GOMAXPROCS는 1 -> 2 -> 4로 처리량이 뚜렷하게 증가했다. GOMAXPROCS=4는 Python 최고 RPS의 약 2.04배다.
- Go process-fanout 최고 RPS는 Python 최고 대비 약 3.30배다. 이 수치는 DB/JWT 없는 password-verify HTTP microbench 기준이다.
- p99/max는 여전히 단일 실행의 꼬리값이다. 다음 판단에는 같은 1000샘플 조건을 3회 반복해서 median-of-runs를 보는 편이 좋다.
