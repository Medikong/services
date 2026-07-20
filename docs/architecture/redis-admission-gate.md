# DropMong Redis admission gate 검토

작성일: 2026-07-05

## 결론

Redis는 DropMong Go MSA에서 최종 쿠폰 발급 원장이 아니라 `coupon-service` 앞단의 admission/gate 계층으로 먼저 도입한다. Postgres는 쿠폰 정책, 발급 원장, 중복 방지 unique constraint, idempotency key의 최종 진실 저장소로 유지한다.

1차 구현은 `coupon-service`만 대상으로 한다. `auth-service`, `user-service`, `backoffice-service`는 Redis 후보가 있지만 이번 구현 범위와 분리한다.

## 현재 병목 지점

현재 쿠폰 발급은 Postgres transaction 하나에서 처리된다.

```text
idempotency key 조회
-> coupon_policies row SELECT ... FOR UPDATE
-> 정책 ready 확인
-> 사용자별 기존 발급 조회
-> total_quantity / issued_count 확인
-> coupon_issuances insert
-> coupon_policies issued_count 증가
-> idempotency key 저장
-> commit
```

정합성 측면에서는 안전하다. 다만 한 정책에 요청이 몰리면 다음 비용이 DB에 집중된다.

- 정상 발급 요청은 모두 같은 `coupon_policies` row lock을 직렬로 기다린다.
- sold-out 요청도 DB transaction과 row lock 경로까지 들어간다.
- 중복 요청은 기존 발급 확인 전 정책 row lock을 잡을 수 있다.
- 앱 replica를 늘려도 DB connection, row lock, DB CPU가 먼저 병목이 될 수 있다.

현재 smoke benchmark는 `tests/scripts/dropmong_api_benchmark.py`의 `coupon.issue` latency로 API 처리 비용을 본다. Redis 효과를 보려면 같은 preset에서 `COUPON_REDIS_GATE_ENABLED=false/true`를 나눠 실행하고, `coupon_redis_gate_total{result}`와 `coupon_db_finalize_total{result}`를 함께 본다.

## Redis gate 역할

Redis gate는 DB 전에 다음 판정을 원자적으로 수행한다.

| 결과 | 의미 | DB 접근 |
| --- | --- | --- |
| `issued_candidate` | Redis 기준 잔여 수량이 있고 중복이 아니다. | DB finalize 수행 |
| `duplicate` | Redis에 이미 발급 결과가 있다. | coupon payload가 있으면 DB 접근 없음 |
| `sold_out` | Redis 잔여 수량이 없다. | DB 접근 없음 |
| `not_ready` | Redis 정책 key가 아직 준비되지 않았다. | `db_fallback`에서는 DB 경로 사용 |
| `redis_unavailable` | Redis command가 실패했다. | `db_fallback` 또는 `fail_closed` 설정에 따름 |

key schema:

```text
coupon:{policyId}:remaining
coupon:{policyId}:issued:{userId}
coupon:{policyId}:idem:{userId}:{idempotencyKey}
```

`Admit`은 Lua script로 `remaining`, 사용자 중복, idempotency 중복을 한 번에 확인한다. `issued_candidate`가 나오면 Redis는 `remaining`을 먼저 감소시키고 `issued`/`idem` key에 `__pending__`을 짧은 TTL로 둔다.

## DB finalize와 보상

Redis gate 통과 후 DB finalize는 기존 `repository.Store.Issue`를 그대로 사용한다. 따라서 최종 정합성은 여전히 Postgres unique constraint와 발급 원장이 보장한다.

보상 규칙:

- DB insert와 transaction이 성공하면 Redis `issued`/`idem` key를 쿠폰 JSON으로 확정한다.
- DB가 duplicate를 반환하면 Redis가 선감소한 `remaining`을 되돌리고 발급 쿠폰 JSON을 저장한다.
- DB sold-out, not-ready, unknown error가 발생하면 Redis pending key를 지우고 `remaining`을 되돌린다.
- Redis 확정 기록 실패는 사용자 응답을 실패로 바꾸지 않는다. DB 원장이 이미 성공했기 때문이다.
- pending TTL이 만료되면 같은 사용자의 재시도는 다시 Redis gate를 탈 수 있지만, DB unique constraint가 중복 발급을 막는다.

Redis 장애 정책:

- 기본값은 `COUPON_REDIS_GATE_FAILURE_MODE=db_fallback`이다. Redis 장애 시 기존 Postgres-only 경로로 처리한다.
- 피크 이벤트에서 DB 보호를 우선할 때는 `fail_closed`로 바꿀 수 있다. 이 경우 Redis 장애 또는 not-ready 상태를 사용자 오류로 반환한다.

## 서비스별 적합성

| 서비스 | Redis 적합성 | 이번 범위 |
| --- | --- | --- |
| `coupon-service` | 높음. 선착순 수량, 중복, idempotency gate로 DB row lock 진입을 줄일 수 있다. | 구현 대상 |
| `auth-service` | 중간. session/authz cache 후보지만 logout, role 변경, token version invalidation이 필요하다. | 문서 후보 |
| `user-service` | 낮음-중간. read-through profile cache 후보지만 현재 조회 비용이 작다. | 비대상 |
| `backoffice-service` | 낮음. 저빈도 write 중심이다. readiness/policy snapshot cache는 후보이다. | 비대상 |
| 공개 drop/product 조회 | 높음. 추후 `drop-service`/`product-service`가 생기면 cache 효과가 크다. | 미래 후보 |

## 로컬/GitOps 실행

로컬 Docker Compose:

```bash
cd service
COUPON_REDIS_GATE_ENABLED=false task test-e2e
COUPON_REDIS_GATE_ENABLED=true task test-e2e
COUPON_REDIS_GATE_ENABLED=true task test-service SERVICE=coupon-service
```

Docker Desktop Kubernetes:

```bash
cd gitops
task validate
task dev
```

`gitops/platform/data/local/valkey.yaml`은 `coupon-redis` Valkey StatefulSet을 로컬 data chart에 추가한다. `values/services/dev/coupon.yaml`은 dev 환경에서만 Redis gate를 켠다. 공통 `values/services/coupon.yaml`의 기본값은 disabled이다.

## 관측 지표

1차 구현에서 추가된 metric:

- `coupon_redis_gate_total{service,result}`
- `coupon_db_finalize_total{service,result}`
- 기존 `coupon_issue_total{service,result}`

metric label에는 `user_id`, `request_id`, `trace_id`, raw path를 넣지 않는다. `result`는 낮은 카디널리티 값만 사용한다.

후속 후보:

- Redis command duration histogram
- Redis unavailable/fallback count 전용 metric
- reconcile 결과 metric
- DB finalize duration histogram

## 남은 검증 질문

- sold-out storm에서 `coupon_db_finalize_total`이 Redis disabled보다 얼마나 줄어드는가?
- duplicate storm에서 Redis coupon JSON hit 이후 DB lock 진입이 실제로 사라지는가?
- Redis pending TTL 만료와 DB commit 지연이 겹칠 때 사용자 응답이 기대 범위 안에 머무는가?
- 피크 이벤트 운영에서는 `db_fallback`과 `fail_closed` 중 무엇을 기본으로 둘 것인가?
