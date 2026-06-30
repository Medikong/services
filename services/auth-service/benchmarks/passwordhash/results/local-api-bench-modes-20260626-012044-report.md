# Local API Runtime Benchmark

- 실행 시각: 2026-06-26T01:39:32
- raw: `local-api-bench-modes-20260626-012044-raw.txt`
- jsonl: `local-api-bench-modes-20260626-012044.jsonl`
- csv: `local-api-bench-modes-20260626-012044.csv`
- endpoint: `POST /bench/password/verify`
- hash: PBKDF2-HMAC-SHA256, 210000 iterations, 고정 fixture
- 범위: 로컬 HTTP API 경로 비교. DB, JWT, Kubernetes, Kong, HPA는 제외.

## fixed_cpu

- 최고 RPS: `go net/http workers=4` -> `157.682 RPS`
- 최저 p95: `go net/http workers=4` -> `134.438ms`
- errors 합계: `4`

### fixed_cpu process results

| case | cpu | rps | mean | p50 | p95 | p99 | max | errors |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| python uvicorn workers=1 | control=not_strict, total=1, per=1, runtime=- | 63.210 | 252.488ms | 246.778ms | 338.679ms | 389.717ms | 477.788ms | 0 |
| python uvicorn workers=1 | control=not_strict, total=2, per=2, runtime=- | 58.765 | 271.409ms | 265.642ms | 350.035ms | 530.179ms | 570.454ms | 0 |
| python uvicorn workers=2 | control=not_strict, total=2, per=1, runtime=- | 53.173 | 300.044ms | 291.746ms | 394.435ms | 623.546ms | 780.236ms | 0 |
| python uvicorn workers=1 | control=not_strict, total=4, per=4, runtime=- | 55.657 | 286.678ms | 284.176ms | 366.544ms | 489.457ms | 566.078ms | 0 |
| python uvicorn workers=2 | control=not_strict, total=4, per=2, runtime=- | 55.366 | 287.994ms | 284.729ms | 366.954ms | 513.687ms | 612.755ms | 0 |
| python uvicorn workers=4 | control=not_strict, total=4, per=1, runtime=- | 61.363 | 259.916ms | 255.600ms | 333.574ms | 499.838ms | 600.624ms | 0 |
| go net/http workers=1 | control=runtime_slots, total=1, per=1, runtime=1 | 40.349 | 394.268ms | 411.077ms | 465.153ms | 611.267ms | 666.271ms | 0 |
| go net/http workers=1 | control=runtime_slots, total=2, per=2, runtime=2 | 77.516 | 205.519ms | 205.198ms | 297.846ms | 376.043ms | 427.859ms | 0 |
| go net/http workers=2 | control=runtime_slots, total=2, per=1, runtime=1 | 78.360 | 203.352ms | 204.394ms | 249.570ms | 311.412ms | 361.024ms | 0 |
| go net/http workers=1 | control=runtime_slots, total=4, per=4, runtime=4 | 143.956 | 110.575ms | 103.986ms | 165.535ms | 368.112ms | 480.243ms | 0 |
| go net/http workers=2 | control=runtime_slots, total=4, per=2, runtime=2 | 151.083 | 105.243ms | 102.293ms | 154.936ms | 181.546ms | 220.422ms | 0 |
| go net/http workers=4 | control=runtime_slots, total=4, per=1, runtime=1 | 157.682 | 100.964ms | 99.505ms | 134.438ms | 150.626ms | 178.170ms | 0 |
| nodejs node:http workers=1 | control=runtime_slots, total=1, per=1, runtime=1 | 18.278 | 868.976ms | 861.880ms | 999.655ms | 1108.972ms | 1119.582ms | 0 |
| nodejs node:http workers=1 | control=runtime_slots, total=2, per=2, runtime=2 | 32.813 | 484.112ms | 469.438ms | 607.940ms | 784.310ms | 820.781ms | 0 |
| nodejs node:http workers=2 | control=runtime_slots, total=2, per=1, runtime=1 | 36.312 | 436.435ms | 434.222ms | 506.577ms | 658.335ms | 747.424ms | 0 |
| nodejs node:http workers=1 | control=runtime_slots, total=4, per=4, runtime=4 | 51.967 | 305.739ms | 299.373ms | 353.967ms | 499.903ms | 562.001ms | 0 |
| nodejs node:http workers=2 | control=runtime_slots, total=4, per=2, runtime=2 | 61.185 | 258.117ms | 249.018ms | 404.483ms | 641.091ms | 736.771ms | 0 |
| nodejs node:http workers=4 | control=runtime_slots, total=4, per=1, runtime=1 | 72.454 | 219.202ms | 220.827ms | 261.491ms | 285.585ms | 322.060ms | 0 |
| nodejs fastify workers=1 | control=runtime_slots, total=1, per=1, runtime=1 | 18.182 | 873.609ms | 859.396ms | 1032.062ms | 1158.203ms | 1172.067ms | 0 |
| nodejs fastify workers=1 | control=runtime_slots, total=2, per=2, runtime=2 | 33.704 | 471.397ms | 464.436ms | 517.908ms | 704.146ms | 718.434ms | 0 |
| nodejs fastify workers=2 | control=runtime_slots, total=2, per=1, runtime=1 | 35.952 | 439.729ms | 448.228ms | 618.319ms | 739.927ms | 801.495ms | 0 |
| nodejs fastify workers=1 | control=runtime_slots, total=4, per=4, runtime=4 | 48.981 | 324.575ms | 305.270ms | 431.850ms | 630.020ms | 655.306ms | 0 |
| nodejs fastify workers=2 | control=runtime_slots, total=4, per=2, runtime=2 | 60.739 | 259.897ms | 259.470ms | 452.690ms | 472.179ms | 543.052ms | 0 |
| nodejs fastify workers=4 | control=runtime_slots, total=4, per=1, runtime=1 | 71.180 | 223.288ms | 216.544ms | 254.081ms | 511.282ms | 541.047ms | 0 |
| rust tiny_http workers=1 | control=not_strict, total=1, per=1, runtime=- | 74.956 | 212.618ms | 171.592ms | 242.561ms | 272.619ms | 10000.360ms | 3 |
| rust tiny_http workers=1 | control=not_strict, total=2, per=2, runtime=- | 83.272 | 191.645ms | 193.113ms | 255.829ms | 277.754ms | 299.117ms | 0 |
| rust tiny_http workers=2 | control=not_strict, total=2, per=1, runtime=- | 81.448 | 195.698ms | 197.340ms | 257.005ms | 279.643ms | 325.847ms | 0 |
| rust tiny_http workers=1 | control=not_strict, total=4, per=4, runtime=- | 76.402 | 208.867ms | 198.615ms | 271.150ms | 336.934ms | 10000.720ms | 1 |
| rust tiny_http workers=2 | control=not_strict, total=4, per=2, runtime=- | 80.017 | 199.415ms | 196.050ms | 261.962ms | 298.522ms | 2648.835ms | 0 |
| rust tiny_http workers=4 | control=not_strict, total=4, per=1, runtime=- | 61.413 | 259.975ms | 254.613ms | 368.910ms | 463.893ms | 551.382ms | 0 |
| rust axum workers=1 | control=runtime_slots, total=1, per=1, runtime=1 | 11.502 | 1380.766ms | 1361.023ms | 1608.345ms | 1670.430ms | 1688.036ms | 0 |
| rust axum workers=1 | control=runtime_slots, total=2, per=2, runtime=2 | 23.236 | 683.542ms | 679.116ms | 728.106ms | 896.610ms | 907.061ms | 0 |
| rust axum workers=2 | control=runtime_slots, total=2, per=1, runtime=1 | 23.175 | 685.401ms | 681.593ms | 733.915ms | 998.243ms | 1034.180ms | 0 |
| rust axum workers=1 | control=runtime_slots, total=4, per=4, runtime=4 | 43.727 | 363.387ms | 361.142ms | 408.874ms | 446.220ms | 457.400ms | 0 |
| rust axum workers=2 | control=runtime_slots, total=4, per=2, runtime=2 | 43.334 | 366.125ms | 362.711ms | 427.577ms | 679.214ms | 694.328ms | 0 |
| rust axum workers=4 | control=runtime_slots, total=4, per=1, runtime=1 | 43.295 | 367.047ms | 367.088ms | 396.122ms | 450.298ms | 490.411ms | 0 |

### fixed_cpu scale-out efficiency

| language | server | total slots | case | rps | vs process=1 |
| --- | --- | ---: | --- | ---: | ---: |
| go | net/http | 1 | workers=1 | 40.349 | 1.00x |
| go | net/http | 2 | workers=1 | 77.516 | 1.00x |
| go | net/http | 2 | workers=2 | 78.360 | 1.01x |
| go | net/http | 4 | workers=1 | 143.956 | 1.00x |
| go | net/http | 4 | workers=2 | 151.083 | 1.05x |
| go | net/http | 4 | workers=4 | 157.682 | 1.10x |
| nodejs | fastify | 1 | workers=1 | 18.182 | 1.00x |
| nodejs | fastify | 2 | workers=1 | 33.704 | 1.00x |
| nodejs | fastify | 2 | workers=2 | 35.952 | 1.07x |
| nodejs | fastify | 4 | workers=1 | 48.981 | 1.00x |
| nodejs | fastify | 4 | workers=2 | 60.739 | 1.24x |
| nodejs | fastify | 4 | workers=4 | 71.180 | 1.45x |
| nodejs | node:http | 1 | workers=1 | 18.278 | 1.00x |
| nodejs | node:http | 2 | workers=1 | 32.813 | 1.00x |
| nodejs | node:http | 2 | workers=2 | 36.312 | 1.11x |
| nodejs | node:http | 4 | workers=1 | 51.967 | 1.00x |
| nodejs | node:http | 4 | workers=2 | 61.185 | 1.18x |
| nodejs | node:http | 4 | workers=4 | 72.454 | 1.39x |
| python | uvicorn | 1 | workers=1 | 63.210 | 1.00x |
| python | uvicorn | 2 | workers=1 | 58.765 | 1.00x |
| python | uvicorn | 2 | workers=2 | 53.173 | 0.90x |
| python | uvicorn | 4 | workers=1 | 55.657 | 1.00x |
| python | uvicorn | 4 | workers=2 | 55.366 | 0.99x |
| python | uvicorn | 4 | workers=4 | 61.363 | 1.10x |
| rust | axum | 1 | workers=1 | 11.502 | 1.00x |
| rust | axum | 2 | workers=1 | 23.236 | 1.00x |
| rust | axum | 2 | workers=2 | 23.175 | 1.00x |
| rust | axum | 4 | workers=1 | 43.727 | 1.00x |
| rust | axum | 4 | workers=2 | 43.334 | 0.99x |
| rust | axum | 4 | workers=4 | 43.295 | 0.99x |
| rust | tiny_http | 1 | workers=1 | 74.956 | 1.00x |
| rust | tiny_http | 2 | workers=1 | 83.272 | 1.00x |
| rust | tiny_http | 2 | workers=2 | 81.448 | 0.98x |
| rust | tiny_http | 4 | workers=1 | 76.402 | 1.00x |
| rust | tiny_http | 4 | workers=2 | 80.017 | 1.05x |
| rust | tiny_http | 4 | workers=4 | 61.413 | 0.80x |

## max_cpu

- 최고 RPS: `go net/http workers=2` -> `261.596 RPS`
- 최저 p95: `go net/http workers=1` -> `94.731ms`
- errors 합계: `11`

### max_cpu process results

| case | cpu | rps | mean | p50 | p95 | p99 | max | errors |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| python uvicorn workers=1 | control=max, total=-, per=-, runtime=- | 74.863 | 213.139ms | 210.007ms | 286.308ms | 331.358ms | 397.516ms | 0 |
| python uvicorn workers=2 | control=max, total=-, per=-, runtime=- | 66.225 | 240.670ms | 231.033ms | 337.048ms | 386.618ms | 445.285ms | 0 |
| python uvicorn workers=4 | control=max, total=-, per=-, runtime=- | 60.763 | 262.696ms | 246.439ms | 419.864ms | 513.363ms | 596.293ms | 0 |
| go net/http workers=1 | control=max, total=-, per=-, runtime=- | 259.505 | 61.163ms | 58.170ms | 94.731ms | 118.194ms | 154.366ms | 0 |
| go net/http workers=2 | control=max, total=-, per=-, runtime=- | 261.596 | 60.893ms | 59.637ms | 100.749ms | 122.825ms | 165.090ms | 0 |
| go net/http workers=4 | control=max, total=-, per=-, runtime=- | 256.150 | 62.263ms | 55.694ms | 122.502ms | 164.140ms | 218.069ms | 0 |
| nodejs node:http workers=1 | control=max, total=-, per=-, runtime=4 | 56.812 | 277.630ms | 275.471ms | 315.234ms | 351.152ms | 440.972ms | 0 |
| nodejs node:http workers=2 | control=max, total=-, per=-, runtime=4 | 83.013 | 191.022ms | 197.479ms | 309.141ms | 363.536ms | 500.663ms | 0 |
| nodejs node:http workers=4 | control=max, total=-, per=-, runtime=4 | 103.905 | 152.950ms | 149.437ms | 258.901ms | 289.724ms | 343.219ms | 0 |
| nodejs fastify workers=1 | control=max, total=-, per=-, runtime=4 | 50.439 | 314.832ms | 307.440ms | 383.336ms | 436.386ms | 463.102ms | 0 |
| nodejs fastify workers=2 | control=max, total=-, per=-, runtime=4 | 82.732 | 191.689ms | 191.423ms | 260.203ms | 308.216ms | 404.762ms | 0 |
| nodejs fastify workers=4 | control=max, total=-, per=-, runtime=4 | 98.962 | 160.755ms | 153.096ms | 280.015ms | 351.578ms | 470.842ms | 0 |
| rust tiny_http workers=1 | control=max, total=-, per=-, runtime=- | 58.923 | 270.376ms | 135.149ms | 283.961ms | 10001.557ms | 10002.072ms | 11 |
| rust tiny_http workers=2 | control=max, total=-, per=-, runtime=- | 75.537 | 211.097ms | 203.373ms | 301.692ms | 417.247ms | 1659.042ms | 0 |
| rust tiny_http workers=4 | control=max, total=-, per=-, runtime=- | 80.671 | 197.739ms | 199.946ms | 256.221ms | 285.496ms | 322.323ms | 0 |
| rust axum workers=1 | control=max, total=-, per=-, runtime=- | 76.842 | 207.483ms | 206.119ms | 263.417ms | 314.876ms | 412.078ms | 0 |
| rust axum workers=2 | control=max, total=-, per=-, runtime=- | 68.114 | 234.175ms | 225.842ms | 336.593ms | 414.826ms | 488.961ms | 0 |
| rust axum workers=4 | control=max, total=-, per=-, runtime=- | 80.512 | 197.941ms | 197.146ms | 257.366ms | 286.731ms | 308.617ms | 0 |

### max_cpu Go GOMAXPROCS reference

| case | cpu | rps | mean | p50 | p95 | p99 | max | errors |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| go net/http GOMAXPROCS=1 | control=runtime_slots, total=-, per=-, runtime=1 | 39.725 | 401.466ms | 413.621ms | 493.016ms | 635.509ms | 832.113ms | 0 |
| go net/http GOMAXPROCS=2 | control=runtime_slots, total=-, per=-, runtime=2 | 78.126 | 203.960ms | 200.834ms | 308.595ms | 387.551ms | 535.181ms | 0 |
| go net/http GOMAXPROCS=4 | control=runtime_slots, total=-, per=-, runtime=4 | 155.080 | 102.498ms | 99.260ms | 157.145ms | 185.622ms | 231.317ms | 0 |

## 해석 메모

- `max_cpu`는 런타임이 가능한 병렬도를 쓰게 둔 로컬 최대 활용 관점이다.
- `fixed_cpu`는 runtime-level slot을 맞춰 process 분할 자체의 효율을 보는 관점이다.
- Python과 Rust `tiny_http`의 fixed CPU는 macOS 로컬에서 엄밀한 CPU 격리가 아니므로 `cpu_control=not_strict`로 표시한다.
- 엄밀한 CPU 격리는 Docker/cpuset 기반 실험에서 별도로 확인해야 한다.
