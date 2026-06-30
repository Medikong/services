# Local API Runtime Benchmark

- 실행 시각: 2026-06-26T02:03:16
- raw: `local-api-bench-ring-modes-20260626-014953-raw.txt`
- jsonl: `local-api-bench-ring-modes-20260626-014953.jsonl`
- csv: `local-api-bench-ring-modes-20260626-014953.csv`
- endpoint: `POST /bench/password/verify`
- hash: PBKDF2-HMAC-SHA256, 210000 iterations, 고정 fixture
- 범위: 로컬 HTTP API 경로 비교. DB, JWT, Kubernetes, Kong, HPA는 제외.

## fixed_cpu

- 최고 RPS: `rust tiny_http workers=1` -> `337.312 RPS`
- 최저 p95: `rust axum workers=1` -> `90.019ms`
- errors 합계: `0`

### fixed_cpu process results

| case | cpu | rps | mean | p50 | p95 | p99 | max | errors |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| python uvicorn workers=1 | control=not_strict, total=1, per=1, runtime=- | 74.373 | 214.367ms | 207.870ms | 297.353ms | 498.718ms | 604.410ms | 0 |
| python uvicorn workers=1 | control=not_strict, total=2, per=2, runtime=- | 77.189 | 206.453ms | 199.966ms | 289.876ms | 332.881ms | 369.409ms | 0 |
| python uvicorn workers=2 | control=not_strict, total=2, per=1, runtime=- | 73.787 | 216.256ms | 214.179ms | 284.018ms | 316.900ms | 344.694ms | 0 |
| python uvicorn workers=1 | control=not_strict, total=4, per=4, runtime=- | 68.814 | 231.686ms | 227.694ms | 315.116ms | 353.592ms | 404.587ms | 0 |
| python uvicorn workers=2 | control=not_strict, total=4, per=2, runtime=- | 73.788 | 216.011ms | 213.566ms | 298.596ms | 350.034ms | 420.722ms | 0 |
| python uvicorn workers=4 | control=not_strict, total=4, per=1, runtime=- | 64.031 | 249.103ms | 240.672ms | 344.111ms | 572.811ms | 669.742ms | 0 |
| go net/http workers=1 | control=runtime_slots, total=1, per=1, runtime=1 | 39.872 | 399.963ms | 412.226ms | 468.920ms | 624.846ms | 686.226ms | 0 |
| go net/http workers=1 | control=runtime_slots, total=2, per=2, runtime=2 | 77.462 | 205.867ms | 200.865ms | 319.242ms | 525.997ms | 689.118ms | 0 |
| go net/http workers=2 | control=runtime_slots, total=2, per=1, runtime=1 | 80.790 | 197.384ms | 199.179ms | 259.590ms | 312.060ms | 363.672ms | 0 |
| go net/http workers=1 | control=runtime_slots, total=4, per=4, runtime=4 | 151.736 | 104.798ms | 97.349ms | 164.214ms | 352.470ms | 441.544ms | 0 |
| go net/http workers=2 | control=runtime_slots, total=4, per=2, runtime=2 | 160.561 | 99.147ms | 95.984ms | 154.125ms | 182.930ms | 251.476ms | 0 |
| go net/http workers=4 | control=runtime_slots, total=4, per=1, runtime=1 | 141.964 | 112.265ms | 107.720ms | 163.889ms | 264.211ms | 361.045ms | 0 |
| nodejs node:http workers=1 | control=runtime_slots, total=1, per=1, runtime=1 | 18.367 | 864.741ms | 856.408ms | 927.807ms | 1157.553ms | 1199.814ms | 0 |
| nodejs node:http workers=1 | control=runtime_slots, total=2, per=2, runtime=2 | 29.202 | 544.035ms | 533.396ms | 609.290ms | 771.669ms | 828.520ms | 0 |
| nodejs node:http workers=2 | control=runtime_slots, total=2, per=1, runtime=1 | 36.579 | 433.536ms | 442.691ms | 487.397ms | 887.413ms | 939.883ms | 0 |
| nodejs node:http workers=1 | control=runtime_slots, total=4, per=4, runtime=4 | 52.292 | 303.934ms | 302.388ms | 341.117ms | 358.999ms | 394.419ms | 0 |
| nodejs node:http workers=2 | control=runtime_slots, total=4, per=2, runtime=2 | 65.798 | 239.746ms | 242.026ms | 422.957ms | 440.130ms | 447.359ms | 0 |
| nodejs node:http workers=4 | control=runtime_slots, total=4, per=1, runtime=1 | 69.197 | 229.341ms | 219.568ms | 283.741ms | 626.507ms | 674.413ms | 0 |
| nodejs fastify workers=1 | control=runtime_slots, total=1, per=1, runtime=1 | 18.350 | 865.423ms | 852.597ms | 980.073ms | 1135.342ms | 1173.284ms | 0 |
| nodejs fastify workers=1 | control=runtime_slots, total=2, per=2, runtime=2 | 33.102 | 479.872ms | 480.421ms | 516.928ms | 539.835ms | 549.677ms | 0 |
| nodejs fastify workers=2 | control=runtime_slots, total=2, per=1, runtime=1 | 36.458 | 435.720ms | 428.234ms | 472.195ms | 814.222ms | 834.930ms | 0 |
| nodejs fastify workers=1 | control=runtime_slots, total=4, per=4, runtime=4 | 51.018 | 311.492ms | 309.810ms | 346.089ms | 376.712ms | 473.546ms | 0 |
| nodejs fastify workers=2 | control=runtime_slots, total=4, per=2, runtime=2 | 60.605 | 260.144ms | 248.370ms | 477.581ms | 657.835ms | 722.552ms | 0 |
| nodejs fastify workers=4 | control=runtime_slots, total=4, per=1, runtime=1 | 72.929 | 217.663ms | 215.832ms | 252.709ms | 263.938ms | 272.676ms | 0 |
| rust tiny_http workers=1 | control=not_strict, total=1, per=1, runtime=- | 258.785 | 61.514ms | 50.847ms | 121.148ms | 155.650ms | 231.969ms | 0 |
| rust tiny_http workers=1 | control=not_strict, total=2, per=2, runtime=- | 319.674 | 49.810ms | 43.133ms | 93.195ms | 117.458ms | 162.533ms | 0 |
| rust tiny_http workers=2 | control=not_strict, total=2, per=1, runtime=- | 286.145 | 55.651ms | 46.021ms | 107.905ms | 144.639ms | 182.214ms | 0 |
| rust tiny_http workers=1 | control=not_strict, total=4, per=4, runtime=- | 337.312 | 47.208ms | 35.246ms | 104.823ms | 138.406ms | 1158.079ms | 0 |
| rust tiny_http workers=2 | control=not_strict, total=4, per=2, runtime=- | 309.676 | 51.464ms | 42.834ms | 101.406ms | 133.734ms | 192.896ms | 0 |
| rust tiny_http workers=4 | control=not_strict, total=4, per=1, runtime=- | 308.523 | 51.701ms | 46.054ms | 97.131ms | 121.362ms | 160.901ms | 0 |
| rust axum workers=1 | control=runtime_slots, total=1, per=1, runtime=1 | 44.455 | 357.271ms | 356.961ms | 375.049ms | 412.174ms | 415.365ms | 0 |
| rust axum workers=1 | control=runtime_slots, total=2, per=2, runtime=2 | 88.925 | 178.580ms | 178.399ms | 185.050ms | 214.425ms | 217.785ms | 0 |
| rust axum workers=2 | control=runtime_slots, total=2, per=1, runtime=1 | 88.008 | 180.405ms | 177.820ms | 184.144ms | 335.764ms | 395.049ms | 0 |
| rust axum workers=1 | control=runtime_slots, total=4, per=4, runtime=4 | 178.435 | 89.019ms | 88.812ms | 90.019ms | 130.786ms | 138.958ms | 0 |
| rust axum workers=2 | control=runtime_slots, total=4, per=2, runtime=2 | 168.909 | 94.094ms | 92.557ms | 102.904ms | 150.662ms | 160.712ms | 0 |
| rust axum workers=4 | control=runtime_slots, total=4, per=1, runtime=1 | 164.410 | 94.627ms | 69.685ms | 264.178ms | 293.906ms | 297.059ms | 0 |

### fixed_cpu scale-out efficiency

| language | server | total slots | case | rps | vs process=1 |
| --- | --- | ---: | --- | ---: | ---: |
| go | net/http | 1 | workers=1 | 39.872 | 1.00x |
| go | net/http | 2 | workers=1 | 77.462 | 1.00x |
| go | net/http | 2 | workers=2 | 80.790 | 1.04x |
| go | net/http | 4 | workers=1 | 151.736 | 1.00x |
| go | net/http | 4 | workers=2 | 160.561 | 1.06x |
| go | net/http | 4 | workers=4 | 141.964 | 0.94x |
| nodejs | fastify | 1 | workers=1 | 18.350 | 1.00x |
| nodejs | fastify | 2 | workers=1 | 33.102 | 1.00x |
| nodejs | fastify | 2 | workers=2 | 36.458 | 1.10x |
| nodejs | fastify | 4 | workers=1 | 51.018 | 1.00x |
| nodejs | fastify | 4 | workers=2 | 60.605 | 1.19x |
| nodejs | fastify | 4 | workers=4 | 72.929 | 1.43x |
| nodejs | node:http | 1 | workers=1 | 18.367 | 1.00x |
| nodejs | node:http | 2 | workers=1 | 29.202 | 1.00x |
| nodejs | node:http | 2 | workers=2 | 36.579 | 1.25x |
| nodejs | node:http | 4 | workers=1 | 52.292 | 1.00x |
| nodejs | node:http | 4 | workers=2 | 65.798 | 1.26x |
| nodejs | node:http | 4 | workers=4 | 69.197 | 1.32x |
| python | uvicorn | 1 | workers=1 | 74.373 | 1.00x |
| python | uvicorn | 2 | workers=1 | 77.189 | 1.00x |
| python | uvicorn | 2 | workers=2 | 73.787 | 0.96x |
| python | uvicorn | 4 | workers=1 | 68.814 | 1.00x |
| python | uvicorn | 4 | workers=2 | 73.788 | 1.07x |
| python | uvicorn | 4 | workers=4 | 64.031 | 0.93x |
| rust | axum | 1 | workers=1 | 44.455 | 1.00x |
| rust | axum | 2 | workers=1 | 88.925 | 1.00x |
| rust | axum | 2 | workers=2 | 88.008 | 0.99x |
| rust | axum | 4 | workers=1 | 178.435 | 1.00x |
| rust | axum | 4 | workers=2 | 168.909 | 0.95x |
| rust | axum | 4 | workers=4 | 164.410 | 0.92x |
| rust | tiny_http | 1 | workers=1 | 258.785 | 1.00x |
| rust | tiny_http | 2 | workers=1 | 319.674 | 1.00x |
| rust | tiny_http | 2 | workers=2 | 286.145 | 0.90x |
| rust | tiny_http | 4 | workers=1 | 337.312 | 1.00x |
| rust | tiny_http | 4 | workers=2 | 309.676 | 0.92x |
| rust | tiny_http | 4 | workers=4 | 308.523 | 0.91x |

## max_cpu

- 최고 RPS: `rust tiny_http workers=1` -> `310.275 RPS`
- 최저 p95: `rust axum workers=1` -> `87.998ms`
- errors 합계: `0`

### max_cpu process results

| case | cpu | rps | mean | p50 | p95 | p99 | max | errors |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| python uvicorn workers=1 | control=max, total=-, per=-, runtime=- | 82.643 | 192.989ms | 188.030ms | 261.370ms | 283.484ms | 317.650ms | 0 |
| python uvicorn workers=2 | control=max, total=-, per=-, runtime=- | 71.680 | 222.507ms | 217.583ms | 287.060ms | 484.486ms | 603.127ms | 0 |
| python uvicorn workers=4 | control=max, total=-, per=-, runtime=- | 75.947 | 209.877ms | 205.262ms | 269.611ms | 515.644ms | 589.062ms | 0 |
| go net/http workers=1 | control=max, total=-, per=-, runtime=- | 262.794 | 60.611ms | 56.418ms | 101.711ms | 139.820ms | 214.687ms | 0 |
| go net/http workers=2 | control=max, total=-, per=-, runtime=- | 212.372 | 74.986ms | 68.515ms | 140.099ms | 201.460ms | 284.838ms | 0 |
| go net/http workers=4 | control=max, total=-, per=-, runtime=- | 279.435 | 57.035ms | 51.097ms | 106.558ms | 141.698ms | 169.005ms | 0 |
| nodejs node:http workers=1 | control=max, total=-, per=-, runtime=4 | 56.472 | 281.147ms | 280.274ms | 303.560ms | 316.646ms | 438.475ms | 0 |
| nodejs node:http workers=2 | control=max, total=-, per=-, runtime=4 | 88.053 | 180.485ms | 179.636ms | 220.217ms | 247.503ms | 310.612ms | 0 |
| nodejs node:http workers=4 | control=max, total=-, per=-, runtime=4 | 108.813 | 146.058ms | 147.778ms | 255.583ms | 292.777ms | 336.092ms | 0 |
| nodejs fastify workers=1 | control=max, total=-, per=-, runtime=4 | 51.565 | 308.225ms | 307.944ms | 344.897ms | 379.683ms | 406.210ms | 0 |
| nodejs fastify workers=2 | control=max, total=-, per=-, runtime=4 | 77.665 | 204.321ms | 206.232ms | 341.467ms | 361.020ms | 416.351ms | 0 |
| nodejs fastify workers=4 | control=max, total=-, per=-, runtime=4 | 105.668 | 150.017ms | 132.738ms | 302.456ms | 340.608ms | 438.643ms | 0 |
| rust tiny_http workers=1 | control=max, total=-, per=-, runtime=- | 310.275 | 51.315ms | 42.568ms | 101.504ms | 126.742ms | 188.922ms | 0 |
| rust tiny_http workers=2 | control=max, total=-, per=-, runtime=- | 296.997 | 53.682ms | 41.767ms | 98.342ms | 134.887ms | 907.660ms | 0 |
| rust tiny_http workers=4 | control=max, total=-, per=-, runtime=- | 297.938 | 53.464ms | 45.694ms | 99.084ms | 133.917ms | 397.947ms | 0 |
| rust axum workers=1 | control=max, total=-, per=-, runtime=- | 298.544 | 53.341ms | 50.543ms | 87.998ms | 124.564ms | 171.240ms | 0 |
| rust axum workers=2 | control=max, total=-, per=-, runtime=- | 296.650 | 53.704ms | 49.605ms | 93.675ms | 127.400ms | 153.254ms | 0 |
| rust axum workers=4 | control=max, total=-, per=-, runtime=- | 306.938 | 51.905ms | 48.311ms | 90.172ms | 112.392ms | 150.756ms | 0 |

### max_cpu Go GOMAXPROCS reference

| case | cpu | rps | mean | p50 | p95 | p99 | max | errors |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| go net/http GOMAXPROCS=1 | control=runtime_slots, total=-, per=-, runtime=1 | 39.918 | 399.278ms | 412.245ms | 468.346ms | 613.600ms | 724.273ms | 0 |
| go net/http GOMAXPROCS=2 | control=runtime_slots, total=-, per=-, runtime=2 | 79.041 | 201.721ms | 201.340ms | 308.169ms | 368.214ms | 416.545ms | 0 |
| go net/http GOMAXPROCS=4 | control=runtime_slots, total=-, per=-, runtime=4 | 151.236 | 105.234ms | 100.002ms | 162.562ms | 204.101ms | 309.327ms | 0 |

## 해석 메모

- `max_cpu`는 런타임이 가능한 병렬도를 쓰게 둔 로컬 최대 활용 관점이다.
- `fixed_cpu`는 runtime-level slot을 맞춰 process 분할 자체의 효율을 보는 관점이다.
- Python과 Rust `tiny_http`의 fixed CPU는 macOS 로컬에서 엄밀한 CPU 격리가 아니므로 `cpu_control=not_strict`로 표시한다.
- 엄밀한 CPU 격리는 Docker/cpuset 기반 실험에서 별도로 확인해야 한다.
