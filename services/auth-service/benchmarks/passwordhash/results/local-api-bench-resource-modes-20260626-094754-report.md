# Local API Runtime Benchmark

- 실행 시각: 2026-06-26T10:01:27
- raw: `local-api-bench-resource-modes-20260626-094754-raw.txt`
- jsonl: `local-api-bench-resource-modes-20260626-094754.jsonl`
- csv: `local-api-bench-resource-modes-20260626-094754.csv`
- endpoint: `POST /bench/password/verify`
- hash: PBKDF2-HMAC-SHA256, 210000 iterations, 고정 fixture
- 범위: 로컬 HTTP API 경로 비교. DB, JWT, Kubernetes, Kong, HPA는 제외.

## fixed_cpu

- 최고 RPS: `rust tiny_http workers=2` -> `359.831 RPS`
- 최저 p95: `rust tiny_http workers=1` -> `82.746ms`
- errors 합계: `0`

### fixed_cpu process results

| case | cpu | rps | p50 | p95 | p99 | max | CPU avg/max | RSS avg/max | startup | size | samples | errors |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| python uvicorn workers=1 | control=not_strict, total=1, per=1, runtime=- | 71.552 | 223.176ms | 321.432ms | 370.004ms | 421.598ms | 29.300/29.300 | 73.188/73.188MB | 93.000ms | - | 1 | 0 |
| python uvicorn workers=1 | control=not_strict, total=2, per=2, runtime=- | 67.020 | 238.012ms | 361.554ms | 407.733ms | 459.242ms | 37.200/37.200 | 77.281/77.281MB | 93.000ms | - | 1 | 0 |
| python uvicorn workers=2 | control=not_strict, total=2, per=1, runtime=- | 70.671 | 215.570ms | 364.758ms | 433.655ms | 512.196ms | - | - | 93.000ms | - | 0 | 0 |
| python uvicorn workers=1 | control=not_strict, total=4, per=4, runtime=- | 63.274 | 249.187ms | 358.048ms | 391.881ms | 478.486ms | 33.900/33.900 | 77.312/77.312MB | 94.000ms | - | 1 | 0 |
| python uvicorn workers=2 | control=not_strict, total=4, per=2, runtime=- | 59.642 | 268.617ms | 360.952ms | 405.949ms | 474.890ms | - | - | 97.000ms | - | 0 | 0 |
| python uvicorn workers=4 | control=not_strict, total=4, per=1, runtime=- | 57.731 | 271.109ms | 371.632ms | 420.226ms | 451.016ms | - | - | 169.000ms | - | 0 | 0 |
| go net/http workers=1 | control=runtime_slots, total=1, per=1, runtime=1 | 41.349 | 400.559ms | 479.123ms | 632.535ms | 685.180ms | 96.284/100.800 | 13.099/15.344MB | 95.000ms | 8.010MB | 44 | 0 |
| go net/http workers=1 | control=runtime_slots, total=2, per=2, runtime=2 | 82.291 | 196.747ms | 294.726ms | 365.757ms | 447.342ms | 187.864/201.400 | 13.391/15.609MB | 92.000ms | 8.010MB | 22 | 0 |
| go net/http workers=2 | control=runtime_slots, total=2, per=1, runtime=1 | 82.156 | 204.162ms | 267.556ms | 323.053ms | 408.684ms | 187.886/200.800 | 23.469/25.812MB | 151.000ms | 8.010MB | 21 | 0 |
| go net/http workers=1 | control=runtime_slots, total=4, per=4, runtime=4 | 161.573 | 97.636ms | 148.817ms | 167.948ms | 187.933ms | 358.133/399.900 | 13.927/15.828MB | 88.000ms | 8.010MB | 12 | 0 |
| go net/http workers=2 | control=runtime_slots, total=4, per=2, runtime=2 | 162.713 | 96.776ms | 151.453ms | 175.235ms | 234.813ms | 358.355/400.100 | 24.253/26.641MB | 157.000ms | 8.010MB | 11 | 0 |
| go net/http workers=4 | control=runtime_slots, total=4, per=1, runtime=1 | 156.919 | 101.874ms | 135.498ms | 155.757ms | 176.518ms | 359.370/396.600 | 44.302/46.750MB | 311.000ms | 8.010MB | 10 | 0 |
| nodejs node:http workers=1 | control=runtime_slots, total=1, per=1, runtime=1 | 19.418 | 827.393ms | 835.140ms | 838.799ms | 846.589ms | 98.697/101.200 | 57.137/59.422MB | 97.000ms | - | 93 | 0 |
| nodejs node:http workers=1 | control=runtime_slots, total=2, per=2, runtime=2 | 32.452 | 488.610ms | 537.673ms | 577.921ms | 605.261ms | 195.464/201.500 | 57.433/59.391MB | 100.000ms | - | 55 | 0 |
| nodejs node:http workers=2 | control=runtime_slots, total=2, per=1, runtime=1 | 38.582 | 414.019ms | 434.753ms | 441.889ms | 447.889ms | 195.502/203.200 | 111.545/115.844MB | 166.000ms | - | 45 | 0 |
| nodejs node:http workers=1 | control=runtime_slots, total=4, per=4, runtime=4 | 56.715 | 280.589ms | 309.891ms | 324.284ms | 353.513ms | 384.350/401.400 | 57.664/59.328MB | 97.000ms | - | 32 | 0 |
| nodejs node:http workers=2 | control=runtime_slots, total=4, per=2, runtime=2 | 62.200 | 242.076ms | 479.698ms | 500.630ms | 515.651ms | 371.964/401.200 | 112.512/116.812MB | 167.000ms | - | 28 | 0 |
| nodejs node:http workers=4 | control=runtime_slots, total=4, per=1, runtime=1 | 73.768 | 212.503ms | 282.840ms | 298.269ms | 364.595ms | 373.955/401.400 | 218.151/228.828MB | 330.000ms | - | 22 | 0 |
| nodejs fastify workers=1 | control=runtime_slots, total=1, per=1, runtime=1 | 19.403 | 827.541ms | 836.752ms | 839.707ms | 840.928ms | 99.131/101.600 | 66.279/69.969MB | 208.000ms | 6.896MB | 93 | 0 |
| nodejs fastify workers=1 | control=runtime_slots, total=2, per=2, runtime=2 | 33.338 | 476.822ms | 524.455ms | 552.222ms | 633.417ms | 196.296/203.500 | 67.392/72.859MB | 209.000ms | 6.896MB | 54 | 0 |
| nodejs fastify workers=2 | control=runtime_slots, total=2, per=1, runtime=1 | 38.396 | 414.674ms | 472.368ms | 474.273ms | 475.860ms | 196.244/204.100 | 131.950/137.422MB | 381.000ms | 6.896MB | 45 | 0 |
| nodejs fastify workers=1 | control=runtime_slots, total=4, per=4, runtime=4 | 50.167 | 316.482ms | 356.171ms | 370.853ms | 392.028ms | 386.694/403.300 | 68.811/74.438MB | 208.000ms | 6.896MB | 36 | 0 |
| nodejs fastify workers=2 | control=runtime_slots, total=4, per=2, runtime=2 | 65.168 | 239.167ms | 402.037ms | 503.578ms | 515.183ms | 375.748/404.200 | 137.336/142.016MB | 392.000ms | 6.896MB | 27 | 0 |
| nodejs fastify workers=4 | control=runtime_slots, total=4, per=1, runtime=1 | 69.843 | 217.420ms | 296.231ms | 395.675ms | 468.144ms | 365.023/398.900 | 258.188/271.594MB | 834.000ms | 6.896MB | 22 | 0 |
| rust tiny_http workers=1 | control=not_strict, total=1, per=1, runtime=- | 341.916 | 35.343ms | 90.412ms | 131.338ms | 2924.599ms | 18.520/61.200 | 2.744/2.766MB | 91.000ms | 0.987MB | 5 | 0 |
| rust tiny_http workers=1 | control=not_strict, total=2, per=2, runtime=- | 356.979 | 37.060ms | 92.188ms | 123.139ms | 178.556ms | 21.620/57.200 | 2.778/2.812MB | 89.000ms | 0.987MB | 5 | 0 |
| rust tiny_http workers=2 | control=not_strict, total=2, per=1, runtime=- | 359.831 | 35.704ms | 92.474ms | 125.859ms | 221.756ms | 4.460/14.800 | 5.134/5.281MB | 164.000ms | 0.987MB | 5 | 0 |
| rust tiny_http workers=1 | control=not_strict, total=4, per=4, runtime=- | 355.932 | 34.996ms | 82.746ms | 107.301ms | 2809.406ms | 20.740/59.200 | 2.734/2.766MB | 86.000ms | 0.987MB | 5 | 0 |
| rust tiny_http workers=2 | control=not_strict, total=4, per=2, runtime=- | 278.514 | 45.347ms | 126.919ms | 166.521ms | 307.263ms | 8.200/26.100 | 5.076/5.219MB | 171.000ms | 0.987MB | 6 | 0 |
| rust tiny_http workers=4 | control=not_strict, total=4, per=1, runtime=- | 229.544 | 55.038ms | 163.783ms | 209.182ms | 281.632ms | 52.567/76.400 | 9.474/9.703MB | 308.000ms | 0.987MB | 6 | 0 |
| rust axum workers=1 | control=runtime_slots, total=1, per=1, runtime=1 | 45.650 | 352.783ms | 356.424ms | 357.393ms | 358.875ms | 96.852/100.300 | 3.145/3.297MB | 94.000ms | 1.709MB | 40 | 0 |
| rust axum workers=1 | control=runtime_slots, total=2, per=2, runtime=2 | 91.185 | 175.039ms | 178.352ms | 183.394ms | 187.008ms | 186.805/200.500 | 3.234/3.406MB | 88.000ms | 1.709MB | 20 | 0 |
| rust axum workers=2 | control=runtime_slots, total=2, per=1, runtime=1 | 90.700 | 176.820ms | 181.649ms | 182.708ms | 184.103ms | 187.400/200.500 | 5.741/5.891MB | 163.000ms | 1.709MB | 19 | 0 |
| rust axum workers=1 | control=runtime_slots, total=4, per=4, runtime=4 | 179.259 | 88.994ms | 90.541ms | 99.858ms | 102.941ms | 352.040/399.800 | 3.323/3.531MB | 96.000ms | 1.709MB | 10 | 0 |
| rust axum workers=2 | control=runtime_slots, total=4, per=2, runtime=2 | 158.042 | 93.650ms | 156.844ms | 179.827ms | 197.732ms | 321.236/387.000 | 5.825/5.969MB | 177.000ms | 1.709MB | 11 | 0 |
| rust axum workers=4 | control=runtime_slots, total=4, per=1, runtime=1 | 156.699 | 95.759ms | 138.098ms | 172.846ms | 214.975ms | 326.850/390.900 | 10.884/11.172MB | 484.000ms | 1.709MB | 10 | 0 |

### fixed_cpu scale-out efficiency

| language | server | total slots | case | rps | vs process=1 |
| --- | --- | ---: | --- | ---: | ---: |
| go | net/http | 1 | workers=1 | 41.349 | 1.00x |
| go | net/http | 2 | workers=1 | 82.291 | 1.00x |
| go | net/http | 2 | workers=2 | 82.156 | 1.00x |
| go | net/http | 4 | workers=1 | 161.573 | 1.00x |
| go | net/http | 4 | workers=2 | 162.713 | 1.01x |
| go | net/http | 4 | workers=4 | 156.919 | 0.97x |
| nodejs | fastify | 1 | workers=1 | 19.403 | 1.00x |
| nodejs | fastify | 2 | workers=1 | 33.338 | 1.00x |
| nodejs | fastify | 2 | workers=2 | 38.396 | 1.15x |
| nodejs | fastify | 4 | workers=1 | 50.167 | 1.00x |
| nodejs | fastify | 4 | workers=2 | 65.168 | 1.30x |
| nodejs | fastify | 4 | workers=4 | 69.843 | 1.39x |
| nodejs | node:http | 1 | workers=1 | 19.418 | 1.00x |
| nodejs | node:http | 2 | workers=1 | 32.452 | 1.00x |
| nodejs | node:http | 2 | workers=2 | 38.582 | 1.19x |
| nodejs | node:http | 4 | workers=1 | 56.715 | 1.00x |
| nodejs | node:http | 4 | workers=2 | 62.200 | 1.10x |
| nodejs | node:http | 4 | workers=4 | 73.768 | 1.30x |
| python | uvicorn | 1 | workers=1 | 71.552 | 1.00x |
| python | uvicorn | 2 | workers=1 | 67.020 | 1.00x |
| python | uvicorn | 2 | workers=2 | 70.671 | 1.05x |
| python | uvicorn | 4 | workers=1 | 63.274 | 1.00x |
| python | uvicorn | 4 | workers=2 | 59.642 | 0.94x |
| python | uvicorn | 4 | workers=4 | 57.731 | 0.91x |
| rust | axum | 1 | workers=1 | 45.650 | 1.00x |
| rust | axum | 2 | workers=1 | 91.185 | 1.00x |
| rust | axum | 2 | workers=2 | 90.700 | 0.99x |
| rust | axum | 4 | workers=1 | 179.259 | 1.00x |
| rust | axum | 4 | workers=2 | 158.042 | 0.88x |
| rust | axum | 4 | workers=4 | 156.699 | 0.87x |
| rust | tiny_http | 1 | workers=1 | 341.916 | 1.00x |
| rust | tiny_http | 2 | workers=1 | 356.979 | 1.00x |
| rust | tiny_http | 2 | workers=2 | 359.831 | 1.01x |
| rust | tiny_http | 4 | workers=1 | 355.932 | 1.00x |
| rust | tiny_http | 4 | workers=2 | 278.514 | 0.78x |
| rust | tiny_http | 4 | workers=4 | 229.544 | 0.64x |

## max_cpu

- 최고 RPS: `rust axum workers=4` -> `356.829 RPS`
- 최저 p95: `rust axum workers=4` -> `83.177ms`
- errors 합계: `0`

### max_cpu process results

| case | cpu | rps | p50 | p95 | p99 | max | CPU avg/max | RSS avg/max | startup | size | samples | errors |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| python uvicorn workers=1 | control=max, total=-, per=-, runtime=- | 62.524 | 256.773ms | 334.936ms | 362.530ms | 388.306ms | 697.454/781.300 | 87.433/87.500MB | 653.000ms | - | 26 | 0 |
| python uvicorn workers=2 | control=max, total=-, per=-, runtime=- | 66.691 | 227.442ms | 365.465ms | 588.720ms | 689.891ms | 22.200/22.200 | 61.969/61.969MB | 95.000ms | - | 1 | 0 |
| python uvicorn workers=4 | control=max, total=-, per=-, runtime=- | 90.211 | 173.403ms | 259.338ms | 291.767ms | 343.510ms | - | - | 88.000ms | - | 0 | 0 |
| go net/http workers=1 | control=max, total=-, per=-, runtime=- | 274.691 | 54.350ms | 95.703ms | 119.990ms | 149.083ms | 636.729/771.700 | 15.728/17.625MB | 520.000ms | 8.010MB | 7 | 0 |
| go net/http workers=2 | control=max, total=-, per=-, runtime=- | 315.674 | 45.861ms | 88.592ms | 106.835ms | 126.907ms | 705.983/879.300 | 28.844/32.328MB | 165.000ms | 8.010MB | 6 | 0 |
| go net/http workers=4 | control=max, total=-, per=-, runtime=- | 317.377 | 42.547ms | 102.458ms | 127.259ms | 183.979ms | 727.360/853.300 | 52.691/55.125MB | 287.000ms | 8.010MB | 5 | 0 |
| nodejs node:http workers=1 | control=max, total=-, per=-, runtime=4 | 53.925 | 292.271ms | 333.671ms | 356.106ms | 460.892ms | 378.588/397.200 | 57.891/59.578MB | 105.000ms | - | 33 | 0 |
| nodejs node:http workers=2 | control=max, total=-, per=-, runtime=4 | 88.447 | 164.237ms | 308.374ms | 329.098ms | 350.633ms | 694.416/762.300 | 113.584/118.500MB | 164.000ms | - | 19 | 0 |
| nodejs node:http workers=4 | control=max, total=-, per=-, runtime=4 | 122.782 | 115.729ms | 245.709ms | 295.861ms | 360.379ms | 818.115/898.500 | 222.007/231.938MB | 302.000ms | - | 13 | 0 |
| nodejs fastify workers=1 | control=max, total=-, per=-, runtime=4 | 51.518 | 287.510ms | 444.515ms | 564.342ms | 684.559ms | 353.126/397.400 | 68.643/74.609MB | 220.000ms | 6.896MB | 34 | 0 |
| nodejs fastify workers=2 | control=max, total=-, per=-, runtime=4 | 80.457 | 172.532ms | 365.034ms | 504.579ms | 569.374ms | 648.505/752.500 | 137.298/142.062MB | 379.000ms | 6.896MB | 21 | 0 |
| nodejs fastify workers=4 | control=max, total=-, per=-, runtime=4 | 121.729 | 129.016ms | 206.111ms | 241.444ms | 309.836ms | 800.123/912.300 | 267.951/274.516MB | 739.000ms | 6.896MB | 13 | 0 |
| rust tiny_http workers=1 | control=max, total=-, per=-, runtime=- | 281.816 | 48.066ms | 113.035ms | 146.430ms | 215.177ms | 20.633/30.300 | 2.807/2.859MB | 427.000ms | 0.987MB | 6 | 0 |
| rust tiny_http workers=2 | control=max, total=-, per=-, runtime=- | 347.540 | 37.908ms | 91.554ms | 119.138ms | 157.363ms | 18.840/53.000 | 5.078/5.203MB | 153.000ms | 0.987MB | 5 | 0 |
| rust tiny_http workers=4 | control=max, total=-, per=-, runtime=- | 351.433 | 36.888ms | 98.705ms | 133.158ms | 179.471ms | 15.475/35.000 | 9.426/9.688MB | 290.000ms | 0.987MB | 4 | 0 |
| rust axum workers=1 | control=max, total=-, per=-, runtime=- | 299.282 | 50.855ms | 90.827ms | 106.240ms | 136.675ms | 603.583/758.200 | 3.828/4.047MB | 570.000ms | 1.709MB | 6 | 0 |
| rust axum workers=2 | control=max, total=-, per=-, runtime=- | 356.384 | 39.527ms | 83.757ms | 106.712ms | 130.365ms | 696.620/863.600 | 6.819/6.953MB | 163.000ms | 1.709MB | 5 | 0 |
| rust axum workers=4 | control=max, total=-, per=-, runtime=- | 356.829 | 38.672ms | 83.177ms | 103.655ms | 134.046ms | 678.940/882.700 | 12.534/12.797MB | 284.000ms | 1.709MB | 5 | 0 |

### max_cpu Go GOMAXPROCS reference

| case | cpu | rps | p50 | p95 | p99 | max | CPU avg/max | RSS avg/max | startup | size | samples | errors |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| go net/http GOMAXPROCS=1 | control=runtime_slots, total=-, per=-, runtime=1 | 41.115 | 402.415ms | 471.977ms | 618.927ms | 701.062ms | 96.245/101.100 | 13.075/14.938MB | 91.000ms | 8.010MB | 44 | 0 |
| go net/http GOMAXPROCS=2 | control=runtime_slots, total=-, per=-, runtime=2 | 82.276 | 194.335ms | 281.732ms | 347.627ms | 391.085ms | 188.936/202.600 | 13.486/15.469MB | 89.000ms | 8.010MB | 22 | 0 |
| go net/http GOMAXPROCS=4 | control=runtime_slots, total=-, per=-, runtime=4 | 161.412 | 95.625ms | 145.201ms | 170.367ms | 211.754ms | 357.625/399.700 | 13.990/15.953MB | 89.000ms | 8.010MB | 12 | 0 |

## 해석 메모

- `max_cpu`는 런타임이 가능한 병렬도를 쓰게 둔 로컬 최대 활용 관점이다.
- `fixed_cpu`는 runtime-level slot을 맞춰 process 분할 자체의 효율을 보는 관점이다.
- Python과 Rust `tiny_http`의 fixed CPU는 macOS 로컬에서 엄밀한 CPU 격리가 아니므로 `cpu_control=not_strict`로 표시한다.
- 리소스 지표는 macOS `ps` 기반 샘플링 근사값이다. CPU%는 순간 샘플 합산값이라 정밀한 CPU cycle 분석이 아니다.
- RSS는 하네스가 띄운 PID와 하위 프로세스의 샘플별 합산값으로 avg/max를 계산한다.
- 엄밀한 CPU 격리는 Docker/cpuset 기반 실험에서 별도로 확인해야 한다.
