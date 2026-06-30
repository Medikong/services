# auth-service

`auth-service`는 Medikong의 인증과 사용자 세션을 담당하는 FastAPI 서비스다. 회원 가입, 로그인, access token 발급, refresh token 교체, 로그아웃, 내 정보 조회, 감사 로그 조회를 제공한다.

## 한눈에 보기

| 항목 | 현재 기준 |
| --- | --- |
| 주요 역할 | 사용자 인증, 토큰 발급/갱신/폐기, 인증 감사 로그 |
| 핵심 API | `POST /auth/login`, `POST /auth/signup`, `POST /auth/refresh`, `POST /auth/logout`, `GET /auth/me` |
| 비밀번호 검증 | PBKDF2 기본, Argon2id 검증 호환 유지 |
| 기준 리소스 | Pod 1개당 CPU request `1000m`, CPU limit 없음 |
| 기대 처리량 | Pod 1개당 `30 login RPS` |
| 기대 latency | `POST /auth/login` p95 `100ms` 이하 목표 |
| 최근 측정 | warmup 이후 30 RPS 구간 p95 `69.9ms`, error rate `0%` |

`1000m`은 메모리 1Gi가 아니라 CPU 1 vCPU request를 뜻한다. 로그인은 비밀번호 검증 때문에 CPU 영향을 크게 받으므로, auth-service의 capacity baseline은 CPU request를 중심으로 본다.

## 주요 API

| Method | Path | 설명 |
| --- | --- | --- |
| `POST` | `/auth/signup` | CUSTOMER 계정을 생성하고 토큰을 발급한다. |
| `POST` | `/auth/login` | 이메일과 비밀번호를 검증하고 access/refresh token을 발급한다. |
| `GET` | `/auth/me` | bearer access token으로 현재 사용자를 조회한다. |
| `POST` | `/auth/refresh` | refresh token을 교체하고 새 토큰 쌍을 발급한다. |
| `POST` | `/auth/logout` | access token과 선택적으로 refresh token을 폐기한다. |
| `GET` | `/auth/audit-logs` | ADMIN 사용자에게 최근 인증 감사 로그를 제공한다. |
| `GET` | `/health` | 서비스 상태를 확인한다. |

## 비밀번호 정책

현재 신규 저장 기본값은 PBKDF2다. Argon2id 구현과 검증 경로는 유지하지만, 티켓팅 피크 로그인 특성상 높은 메모리 비용이 운영 병목이 될 수 있어 기본 저장 방식으로 적용하지 않는다.

| 항목 | 상태 |
| --- | --- |
| 신규 비밀번호 hash | PBKDF2 |
| legacy PBKDF2 hash 검증 | 지원 |
| Argon2id hash 검증 | 지원 |
| 알 수 없는 hash scheme | 명확히 실패 |
| 로그/trace 노출 | 이메일, 비밀번호, hash 원문, token 원문은 남기지 않음 |

## 성능 기준

capacity baseline은 auth-service 단일 Pod 기준으로 측정한다. 현재 운영 기준치는 아래처럼 둔다.

| 항목 | 기준 |
| --- | --- |
| replica | `1` |
| CPU request | `1000m` |
| CPU limit | 없음 |
| HPA | disabled |
| 목표 처리량 | `30 login RPS` |
| 목표 p95 | `100ms` 이하 |
| target utilization | `70%` |

최근 auth-only capacity baseline은 `10 -> 30 -> 40 RPS` 순서로 실행했다. `10 RPS`는 password verify, DB connection, Python worker를 데우기 위한 warmup step이며 CPU request 산정에서는 제외한다.

| target RPS | 역할 | p50 | p95 | p99 | error rate | CPU avg | CPU request 후보 |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 10 | warmup | 52.7ms | 78.6ms | 228.8ms | 0% | 164.8m | 제외 |
| 30 | 기준 처리량 | 51.5ms | 69.9ms | 117.4ms | 0% | 710.2m | 1015m |
| 40 | 상단 후보 | 50.9ms | 67.6ms | 133.3ms | 0% | 1597.1m | 2282m |

따라서 현재 결론은 다음과 같다.

| 질문 | 답 |
| --- | --- |
| Pod 1개, CPU request `1000m`에서 30 login RPS가 가능한가? | 가능. warmup 이후 p95 `69.9ms`, error rate `0%`로 통과했다. |
| 30 login RPS 기준으로 CPU request를 올려야 하나? | 당장은 `1000m` 유지가 가능하다. 다만 여유가 크지는 않다. |
| 40 login RPS를 Pod 1개가 계속 처리해야 하나? | `1000m`은 낮다. 단일 Pod 기준이면 약 `2300m` 후보가 나온다. |
| 운영 방향은? | Pod를 크게 키우기보다 replica를 늘려 Pod당 login RPS를 30 이하로 낮추는 쪽이 우선이다. |

상세 근거는 workspace 문서에 남긴다.

- `/Users/danghamo/Documents/gituhb/medikong/workspace/docs/evidence/loadtest/capacity-baseline/reports/auth-service-1000m-warmup-2026-06-20/README.md`
- `/Users/danghamo/Documents/gituhb/medikong/workspace/docs/evidence/loadtest/capacity-baseline/reports/auth-service-1000m-warmup-2026-06-20/k6-summary-auth-steps.json`

## 로컬 실행

```bash
cd /Users/danghamo/Documents/gituhb/medikong/service/services/auth-service
uv run python cmd/server/main.py
```

## 테스트

```bash
cd /Users/danghamo/Documents/gituhb/medikong/service/services/auth-service
uv run pytest tests/test_auth.py
```

PBKDF2 verify 동시성 벤치마크는 명시적으로 켜서 실행한다.

```bash
cd /Users/danghamo/Documents/gituhb/medikong/service/services/auth-service
AUTH_PBKDF2_CONCURRENCY_BENCHMARK=1 uv run pytest tests/test_pbkdf2_verify_concurrency_benchmark.py -s
```

Python/Go 함수 단위 비교는 같은 PBKDF2-SHA256 fixture를 사용한다.

```bash
cd /Users/danghamo/Documents/gituhb/medikong/service/services/auth-service
uv run pytest tests/test_password_hash_function_contract.py
AUTH_FUNCTION_BENCHMARK=1 uv run pytest tests/test_password_hash_function_contract.py -s

cd /Users/danghamo/Documents/gituhb/medikong/service/services/auth-service/benchmarks/passwordhash
go test ./...
go test -bench=. -benchmem
```

Python/FastAPI, Go, Node.js, Rust HTTP 서버의 로컬 API 마이크로벤치는 Kubernetes, HPA, Kong, Service 경로를 제외하고 서버 런타임 비용을 빠르게 비교한다. 성능 비교 클라이언트는 Go로 작성한 `benchclient`를 공통으로 사용한다.

```bash
cd /Users/danghamo/Documents/gituhb/medikong/service/services/auth-service
uv run pytest tests/test_password_bench_api.py

cd /Users/danghamo/Documents/gituhb/medikong/service/services/auth-service/benchmarks/passwordhash
go test ./...
node --test node/server.test.mjs
cd node-fastify && npm install && npm test && cd ..
cargo test --manifest-path rust-server/Cargo.toml
cargo test --manifest-path rust-axum-server/Cargo.toml
MODE=max_cpu REQUESTS=40 CONCURRENCY=4 scripts/run-local-api-bench.sh
MODE=fixed_cpu TOTAL_CPU_SLOTS=4 REQUESTS=40 CONCURRENCY=4 scripts/run-local-api-bench.sh
MODE=all REQUESTS=40 CONCURRENCY=4 scripts/run-local-api-bench.sh
```

벤치 결과 JSON은 평균뿐 아니라 `min_ms`, `p50_ms`, `p95_ms`, `p99_ms`, `max_ms`, `errors`, `throughput_rps`를 포함한다. 또한 `mode`, `cpu_control`, `total_cpu_slots`, `per_process_cpu_slots`, `runtime_slots`를 기록해 CPU 관점을 분리한다.

로컬 벤치 하네스는 서버 실행 중 macOS `ps` 기반 리소스 샘플링도 함께 수행한다. process fanout 케이스는 하네스가 띄운 서버 PID와 하위 프로세스의 CPU%/RSS를 샘플별로 합산한 뒤 avg/max를 기록한다.

| 리소스 필드 | 의미 |
| --- | --- |
| `server_cpu_percent_avg` / `server_cpu_percent_max` | 벤치 실행 중 서버 PID들의 `ps` CPU% 합산 avg/max |
| `server_rss_mb_avg` / `server_rss_mb_max` | 벤치 실행 중 서버 PID들의 RSS 합산 avg/max |
| `server_process_count_max` | 샘플링 중 관측된 서버 PID + 하위 프로세스 수의 최대값 |
| `resource_sample_count` | 리소스 샘플 수 |
| `resource_sampler` | 현재는 `ps` |
| `startup_time_ms` | 서버 시작부터 `/health` 성공까지 걸린 시간 |
| `binary_size_mb` | Go/Rust 빌드 산출물 또는 Fastify 임시 bundle 크기. Python/기본 Node는 `null` |

이 리소스 지표는 정밀 profiler가 아니라 경향 비교용 근사값이다. CPU%는 macOS `ps` 순간 샘플이므로 cycle-level 분석이나 throttling 판단에는 쓰지 않는다. RSS는 런타임별 상주 메모리 차이와 process fanout 시 메모리 증가 추세를 보는 용도로 사용한다. 더 엄밀한 CPU 고정과 context switch/power 분석은 Docker/cpuset 또는 별도 관측성 도구 단계에서 다룬다.

| 모드 | 의미 |
| --- | --- |
| `max_cpu` | 각 런타임이 기본 병렬도를 가능한 만큼 쓰게 둔 로컬 최대 활용 실험 |
| `fixed_cpu` | 총 CPU slot 예산을 정하고 process 분할 자체의 효율을 보는 실험 |
| `all` | `max_cpu`와 `fixed_cpu`를 모두 실행 |

`fixed_cpu` 기본 조합은 `total_cpu_slots=1/2/4`를 기준으로 가능한 process 분할만 실행한다. 예를 들어 `total_cpu_slots=4`에서는 `workers=1/per=4`, `workers=2/per=2`, `workers=4/per=1`을 실행한다.

기본 순회는 다음 축을 분리한다.

| 축 | 의미 |
| --- | --- |
| Python `process` | `uvicorn --workers 1/2/4` |
| Go `process-fanout` | Go HTTP 서버 프로세스 1/2/4개를 여러 포트로 띄우고 client가 round-robin 호출 |
| Node.js `process-fanout` | Node HTTP 서버 프로세스 1/2/4개를 여러 포트로 띄우고 client가 round-robin 호출 |
| Node.js Fastify `process-fanout` | Fastify 서버 프로세스 1/2/4개를 여러 포트로 띄우고 client가 round-robin 호출 |
| Rust `process-fanout` | Rust HTTP 서버 프로세스 1/2/4개를 여러 포트로 띄우고 client가 round-robin 호출 |
| Rust Axum `process-fanout` | Axum/Tokio 서버 프로세스 1/2/4개를 여러 포트로 띄우고 client가 round-robin 호출 |
| Go `gomaxprocs` | Go HTTP 서버 단일 프로세스에서 `GOMAXPROCS=1/2/4` |

`fixed_cpu`에서 Go는 `GOMAXPROCS`, Node.js/Fastify는 `UV_THREADPOOL_SIZE`, Axum은 Tokio worker/blocking thread 설정을 per-process slot에 맞춘다. Python과 Rust `tiny_http`는 macOS 로컬에서 엄밀한 CPU slot 제어가 어려워 `cpu_control=not_strict`로 표시한다.

`uvicorn --workers`, 서버 프로세스 수, `GOMAXPROCS`는 같은 의미가 아니다. Node.js는 async `crypto.pbkdf2`를 사용하므로 `UV_THREADPOOL_SIZE`도 결과에 영향을 줄 수 있다. Fastify는 Node 생태계의 빠른 HTTP 프레임워크 후보이고, Axum은 Tokio/Tower 기반 Rust 후보로 둔다. 이 벤치는 실제 운영 처리량 결론이 아니라 각 HTTP 런타임의 로컬 경로 차이를 빠르게 보는 사전 실험이다. 운영 기준은 별도의 Kubernetes 기반 loadtest 결과로 판단한다.

raw 결과를 저장한 뒤 보고서를 만들 때는 다음 명령을 사용한다.

```bash
cd /Users/danghamo/Documents/gituhb/medikong/service/services/auth-service/benchmarks/passwordhash
scripts/write-local-api-bench-report.py results/<raw-output>.txt
```
