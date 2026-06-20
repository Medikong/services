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
uv run uvicorn app.main:app --reload
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
