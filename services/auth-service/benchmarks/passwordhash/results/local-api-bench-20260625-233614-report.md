# Auth Password Verify Local API Benchmark

- 실행 시각: 2026-06-25T23:36:55
- raw: `local-api-bench-20260625-233614-raw.txt`
- jsonl: `local-api-bench-20260625-233614.jsonl`
- csv: `local-api-bench-20260625-233614.csv`
- 요청 수: 100 per case
- 동시성: 16
- endpoint: `POST /bench/password/verify`
- hash: PBKDF2-HMAC-SHA256, 210000 iterations, 고정 fixture
- 범위: 로컬 HTTP API 경로 비교. DB, JWT, Kubernetes, Kong, HPA는 제외.

## 전체 요약

- 전체 최고 RPS: `go process-fanout workers=4 gomaxprocs=None` -> `221.107 RPS`
- 전체 최저 p95: `go process-fanout workers=2 gomaxprocs=None` -> `109.639ms`
- Python 최고 RPS: workers=1 -> `69.415 RPS`
- Go process-fanout 최고 RPS: workers=4 -> `221.107 RPS`, Python 최고 대비 `3.19x`
- Go GOMAXPROCS 최고 RPS: GOMAXPROCS=4 -> `134.738 RPS`, Python 최고 대비 `1.94x`
- 모든 케이스 errors=0

### Python Uvicorn process workers

| case | rps | mean | p50 | p95 | p99 | max | errors |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| workers=1 | 69.415 | 223.469ms | 226.387ms | 293.282ms | 308.326ms | 335.218ms | 0 |
| workers=2 | 65.726 | 236.721ms | 240.644ms | 297.227ms | 313.307ms | 320.600ms | 0 |
| workers=4 | 62.619 | 244.461ms | 226.296ms | 392.830ms | 422.373ms | 464.082ms | 0 |

### Go net/http process fanout

| case | rps | mean | p50 | p95 | p99 | max | errors |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| workers=1 | 166.500 | 92.385ms | 78.222ms | 194.116ms | 227.905ms | 247.795ms | 0 |
| workers=2 | 219.240 | 69.807ms | 65.487ms | 109.639ms | 136.943ms | 137.268ms | 0 |
| workers=4 | 221.107 | 68.299ms | 66.266ms | 114.351ms | 129.305ms | 170.047ms | 0 |

### Go net/http single process GOMAXPROCS

| case | rps | mean | p50 | p95 | p99 | max | errors |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| GOMAXPROCS=1 | 38.031 | 407.945ms | 415.144ms | 456.760ms | 521.356ms | 548.251ms | 0 |
| GOMAXPROCS=2 | 70.750 | 220.690ms | 217.426ms | 347.755ms | 375.857ms | 399.906ms | 0 |
| GOMAXPROCS=4 | 134.738 | 114.129ms | 104.561ms | 175.130ms | 216.719ms | 234.507ms | 0 |

## 해석 메모

- 이 수치는 단일 로컬 실행 결과다. 운영 처리량 결론이 아니라 Python/FastAPI와 Go `net/http`의 로컬 HTTP 경로 차이를 보는 사전 비교값이다.
- 이번 표본에서는 Python Uvicorn worker를 1에서 4로 늘려도 RPS가 오르지 않았고 p95/p99는 악화됐다. PBKDF2 비용, 프로세스 스케줄링, 표본 크기 영향이 섞일 수 있어 반복 측정이 필요하다.
- Go process-fanout은 1개에서 2개로 늘 때 RPS가 크게 올랐고, 4개는 2개와 거의 비슷했다. 이 머신/동시성에서는 2개 부근에서 이미 포화에 가까운 신호로 볼 수 있다.
- Go 단일 프로세스 GOMAXPROCS는 1 -> 2 -> 4로 갈수록 처리량이 올라갔다. `GOMAXPROCS=4`도 Go process-fanout 2/4보다 낮으므로, 단일 프로세스 런타임 병렬도와 다중 프로세스 fanout은 별도 축으로 봐야 한다.
- p99와 max는 요청 100개 표본의 꼬리값이므로 방향성 확인용이다. 최종 판단 전에는 최소 3회 반복과 requests 증가가 필요하다.
