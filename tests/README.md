# 테스트 실행 가이드

이 프로젝트의 테스트 진입점은 루트 `Taskfile.yml`이다. Go 공용 기반 구조와 auth/reference service, Python 기반 catalog/order/payment/notification 구매 기능을 함께 검증한다.

업무 흐름을 사람이 직접 검증하거나 장애를 주입해 확인하는 절차는 배포/인프라 repo에서 별도 문서로 관리한다.

## 테스트 범위

| 구분 | 도구 | 대상 |
| --- | --- | --- |
| Go 단위 테스트 | Go test | `packages/go-*`, `services/go-reference-service` |
| Go 통합 테스트 | Go test + `integration` build tag | reference service와 공용 패키지의 조립 경계 |
| Go 벤치마크 | Go test + `benchmark` build tag | Go handler, 공용 패키지, 핵심 경로 성능 |
| Python 단위 테스트 | Docker Python pytest 러너 | `catalog-service`, `order-service`, `payment-service`, `notification-service`, `interest-service` |
| Purchase E2E | Docker Compose, PostgreSQL, Kafka, Docker Newman 컨테이너 | catalog/order/payment/notification 구매, 결제 실패, 품절/동시성 검증 |
| Auth E2E | Docker Compose, Envoy, auth-service/worker, PostgreSQL, Redis, Kafka, mock Provider, Newman | 인증 사용자 여정, Gateway JWT/JWKS, Session, Provider/Outbox 장애와 복구 |
| Observability E2E | Docker Compose, OpenTelemetry Collector, Tempo, Grafana, Python smoke 컨테이너 | Go inbound request span이 OTLP -> Collector -> Tempo 경로로 적재되는지 검증 |
| Kubernetes 정책 E2E | 별도 Kubernetes scope | NetworkPolicy, AuthorizationPolicy, Istio Ingress 객체 자체 검증 |

## 폴더 구조

```text
tests/
  docker/
    Dockerfile
  e2e/
    docker-compose.yml
    observability/
      docker-compose.yml
      otel-collector.yml
      tempo.yml
      grafana/
        provisioning/
          datasources/
            tempo.yml
      scripts/
        trace-smoke.py
    postgres-init/
      01-create-databases.sql
    scenarios/
      auth/
        auth.postman_collection.json
      01-drop-catalog-smoke.postman_collection.json
      02-order-create.postman_collection.json
      03-payment-approve.postman_collection.json
      04-customer-drop-purchase-happy-path.postman_collection.json
      05-payment-failure-flow.postman_collection.json
      06-sold-out-concurrency-flow.postman_collection.json
      09-purchase-kafka-trace-smoke.postman_collection.json
    newman/
      docker.postman_environment.json
```

테스트 실행 Task 본문은 `tests/Taskfile.yml`에 두고, 루트 `Taskfile.yml`은 같은 명령 이름으로 위임한다.

## 로컬 Go 테스트

Go 단위 테스트는 루트에서 실행한다.

```bash
task test-go-unit
```

통합 테스트는 `integration` build tag로 분리한다.

```bash
task test-go-integration
```

벤치마크는 `benchmark` build tag로 분리한다.

```bash
task test-go-benchmark
task test-go-benchmark GO_BENCH_TIME=3s
```

## 로컬 Python 단위 테스트

루트에서 전체 Python 서비스 테스트를 실행한다. 테스트 대상은 `config/services.yml`의 `tests` 목록을 따른다.

```bash
task test-unit
```

단일 서비스만 확인할 때는 서비스 전체 이름이나 짧은 이름을 사용할 수 있다.

```bash
task test-service SERVICE=order-service
task test-service SERVICE=order
```

## interest-service PostgreSQL 통합 테스트

`services/interest-service/tests/integration/`은 실제 PostgreSQL 16이 있어야만 도는 테스트다(전환율/최근활동게이트/폴백티어 정렬, 동시 첫 찜의 UniqueConstraint 충돌 처리 등 — 인메모리 단위 테스트로는 검증 불가능한 실제 DB 제약/쿼리 동작). `test-purchase-postgres-integration`과 같은 패턴으로 Postgres 컨테이너만 띄우고 별도 test-runner 이미지로 실행한다.

```bash
task test-interest-postgres-integration
```

`TEST_DATABASE_URL`이 없으면 스킵되도록 각 테스트가 `pytest.mark.skipif`로 가드돼 있어서, 로컬에서 직접 지정해도 실행할 수 있다.

```bash
TEST_DATABASE_URL=postgresql+asyncpg://<user>:<password>@localhost:5432/<database> \
  uv run --group test pytest tests/integration/test_ranking_postgres.py
```

## 공통 E2E 진입점

구매와 인증 E2E의 사용자 진입점은 하나다. `SCENARIO`를 생략하면 구매 컬렉션과 인증 컬렉션 전체를 차례로 실행한다.

```bash
task test-e2e
```

구매 시나리오는 기존 파일 이름, 인증 시나리오는 `auth/<folder>` 형식으로 선택한다. 인증 단일 실행은 등록 시나리오가 필요한 경우 등록을 준비 단계로 함께 실행한다.

```bash
task test-e2e SCENARIO=04-customer-drop-purchase-happy-path
task test-e2e SCENARIO=auth/jwks-jwt-gateway
task test-e2e SCENARIO=auth/outbox-broker-recovery
```

모든 실행은 같은 공통 Compose와 `scripts`, `newman`, `postgres-init` 구성을 사용한다. 종료 성공 여부와 관계없이 해당 Compose project의 container, volume, network를 제거하고 잔존 리소스가 있으면 실패로 판정한다. JUnit 결과는 `tests/e2e/newman/reports/`에 저장한다.

기본 Email/SMS는 비밀값 없이 실행할 수 있는 mock Provider를 사용한다. 실제 Provider sandbox와 Kubernetes NetworkPolicy/AuthorizationPolicy 객체는 이 Docker Compose 결과에 포함하지 않으며, 별도 secret과 Kubernetes 환경이 있는 명시적 검증 범위로 남긴다.

## Purchase E2E

구매 흐름은 위 공통 진입점에서 기존 Newman 컬렉션을 이동하지 않고 실행한다.

정상 구매의 Kafka producer/consumer span graph까지 확인할 때는 다음 명령을 사용한다.

```bash
task tests:purchase-e2e-with-kafka-traces
```

이 명령은 고유 request ID를 사용하는 `04-customer-drop-purchase-happy-path`와 `09-purchase-kafka-trace-smoke`를 실행한다. 최종 post-security/post-distinct 재실행의 Newman CLI 결과는 `04`가 6 requests / 12 assertions / failures 0, `09`가 5 requests / 14 assertions / failures 0이다. `09`의 bounded polling은 Tempo indexing 상태에 따라 검색 request와 그 request에서 실행되는 assertion을 반복하므로 정확한 requests/assertions 합계는 실행마다 달라질 수 있다. 통과 불변 조건은 failures 0, 서로 다른 order/payment root trace ID, 두 root에 걸친 아래 6개 필수 service/span pair다.

| Trace root | 필수 service/span pair |
| --- | --- |
| order root | `order-service` `kafka.produce order.created`, `payment-service` `kafka.consume order.created` |
| payment root | `payment-service` `kafka.produce payment.approved`, `order-service` `kafka.consume payment.approved`, `order-service` `kafka.produce notification.requested`, `notification-service` `kafka.consume notification.requested` |

최종 재실행에서는 두 trace ID가 서로 달랐고 6개 pair가 모두 통과했다. Cleanup도 exit 0, 잔존 container 0, volume 0으로 끝났다. 전체 구매 여정은 단일 trace로 판정하지 않는다.

정상 구매와 결제 실패의 Loki log correlation은 다음 명령으로 자동 판정한다.

```bash
task tests:purchase-e2e-with-log-correlation
```

이 gate는 HTTP와 Kafka JSON 로그의 correlation/trace 연결, bounded failure code, 민감 필드 부재, 낮은 cardinality의 Loki label을 확인하고 container, volume, network와 임시 build context를 정리한다.

정상 구매 후 notification business metric과 Kafka consumer lag까지 확인할 때는 다음 G005 gate를 사용한다.

```bash
task tests:purchase-e2e-with-notification-metrics
task purchase-e2e-with-notification-metrics
```

루트 wrapper는 위 `tests:` task를 호출한다. gate는 깨끗한 임시 context를 만들고 host port를 모두 ephemeral port(`CATALOG_SERVICE_PORT=0`, `ORDER_SERVICE_PORT=0`, `PAYMENT_SERVICE_PORT=0`, `NOTIFICATION_SERVICE_PORT=0`)로 지정한 뒤, `04-customer-drop-purchase-happy-path`, `10-notification-metrics-happy`, `11-notification-metrics-replay` 순서로 실행한다. Newman 결과는 `10`이 1 request / 5 assertions / 0 failures, `11`이 2 requests / 7 assertions / 0 failures다.

`notification-service`의 outcome metric은 `service_name`, `service_version`, `service_environment` 같은 저카디널리티 라벨을 사용하는 business counter다. 정상 구매 뒤 `10`에서 기대하는 값은 다음과 같다.

| Metric | 값 |
| --- | ---: |
| `notification_requested_events_consumed_total` | 1 |
| `notifications_created_total` | 1 |
| `notification_requested_events_replayed_total` | 0 |
| `notification_requested_events_invalid_total` | 0 |

consumer lag는 notification 애플리케이션의 local gauge로 판정하지 않는다. Kafka 안에서 다음 consumer group과 topic을 `kafka-consumer-groups.sh --describe`로 조회한다.

```bash
docker compose -p dropmong-e2e-notification-metrics -f tests/e2e/docker-compose.yml exec -T kafka \
  /opt/kafka/bin/kafka-consumer-groups.sh \
  --bootstrap-server kafka:29092 \
  --group notification-service-notification-requested \
  --describe
```

`notification-service-notification-requested` group의 `notification.requested` topic은 정상 구매 뒤 committed offset / log-end offset / lag가 `1 / 1 / 0`이어야 한다. 이후 같은 유효 이벤트를 두 번 추가 발행하고 `11`을 실행하면 metric은 `3 / 2 / 1 / 0`(`consumed / created / replayed / invalid`)이어야 하며, duplicate event 하나당 notification은 정확히 1개만 남아야 한다. 같은 Kafka 조회 결과도 `3 / 3 / 0`이어야 한다.

gate 종료 시 `docker compose ... down -v --remove-orphans` cleanup으로 container, volume, network가 각각 0개이고 임시 context가 남지 않아야 한다(`temp context=false`).

### 최종 내부 구매 회귀

루트의 다음 명령 하나로 Gateway를 제외한 내부 구매 회귀를 실행한다.

```bash
task purchase-internal-regression
```

이 명령은 커밋된 HEAD를 추적 파일만 포함하는 깨끗한 복제 context로 옮기고, 서로 다른 UUID를 사용하는 임시 Compose project에서 다음 11개 gate를 순서대로 실행한다. 각 gate는 종료 코드와 실행 시간을 출력하며, 첫 실패의 종료 코드를 그대로 반환하고 이후 gate는 실행하지 않는다. 정리는 성공과 실패 모두에서 실행한다.

실패를 수정한 뒤 이미 통과한 gate를 다시 실행하지 않으려면 `INTERNAL_REGRESSION_START_GATE=<1-11>`로 시작 번호를 지정한다. 이 옵션도 새 clean clone을 만들고 지정 번호 이후만 순서대로 실행한다.

1. `test-services`: catalog, order, payment, notification 단위 회귀
2. `test-purchase-contracts`: 공유 구매 event와 OpenAPI 계약
3. `test-purchase-postgres-integration`: PostgreSQL 16 migration 및 필수 통합 검사
4. `purchase-e2e-with-metrics`: 정상 구매, 결제 실패, 순차 시나리오와 비즈니스 지표
5. `purchase-e2e-concurrency`: 별도 병렬 smoke의 품절 동시성 및 초과 판매 방지
6. `payment-failure-idempotency`: 중복 HTTP 요청과 Kafka 재전달의 결제 실패 멱등성
7. `purchase-lifecycle-e2e`: Outbox 복구, 만료와 지연 승인, Catalog 투영, 취소와 환불, 유형화 알림 생명주기
8. `purchase-e2e-with-traces`: HTTP 추적
9. `purchase-e2e-with-kafka-traces`: Kafka producer/consumer 추적 그래프
10. `purchase-e2e-with-log-correlation`: 정상 구매와 결제 실패의 Loki 상관관계
11. `purchase-e2e-with-notification-metrics`: notification 지표, 재처리 분류, Kafka consumer 지연 회복

모든 E2E 공개 port는 `127.0.0.1`에만 바인딩한다. 각 gate는 고유 Compose project와 test-runner image prefix를 사용한다. 종료 시 해당 실행이 소유한 container, network, volume, image와 임시 clone을 정리하고 각 잔여 개수가 0인지 검사한다.

이 회귀는 내부 구매 서비스의 단위·계약·PostgreSQL·Kafka·관측성 범위만 검증한다. Gateway JWT와 외부 header 위조 차단, UI, 운영 배포 준비, CloudNativePG 구성은 제외하며 이 결과로 해당 범위가 준비됐다고 주장하지 않는다.

Purchase E2E는 Compose 네트워크 DNS로 `catalog-service`, `order-service`, `payment-service`, `notification-service`를 직접 호출한다. 기본 URL은 다음과 같다.

| 서비스 | 기본 URL |
| --- | --- |
| `catalog-service` | `http://catalog-service:8081` |
| `order-service` | `http://order-service:8082` |
| `payment-service` | `http://payment-service:8083` |
| `notification-service` | `http://notification-service:8084` |

Python 구매 서비스는 내부 테스트 context에서 신뢰된 `X-User-Id`와 저장된 주문·결제 소유자를 비교한다. `X-User-Role`이나 `X-User-Email`은 권한 근거로 사용하지 않는다. Auth E2E의 보호 echo는 외부에서 보낸 identity header가 제거되는지 확인하지만, 실제 Istio/Kubernetes 정책 객체까지 검증한 결과는 아니다.

## 로컬 Observability E2E

`task tests:test-observability-e2e`는 기능 시나리오 검증과 별개로 OpenTelemetry trace 수집 경로만 확인한다.

```bash
task tests:test-observability-e2e
```

이 테스트가 확인하는 경로는 다음과 같다.

```text
Go OpenTelemetry instrumentation
-> OTLP
-> OpenTelemetry Collector OTLP receiver
-> Tempo
```

`/healthz`, `/readyz`, `/metrics`는 trace 제외 기본값이다. smoke는 admin 포트의 세 endpoint에 고유 `X-Request-Id`를 붙여 호출한 뒤 Tempo에서 해당 trace가 없어야 한다고 확인한다. trace 생성 요청은 `go-reference-service`의 감사 예제 API를 호출한다.

## CI

`.github/workflows/service-tests.yml`은 PR 변경 경로를 기준으로 테스트 대상만 만든다. `services/<service>/**` 변경은 해당 서비스 테스트와 이미지를 선택하고, `tests/**`, `packages/**`, `Taskfile.yml` 변경은 넓은 범위의 검증을 실행한다.

`.github/workflows/e2e.yml`은 Docker Compose 기반 E2E stack을 실행한다. 공통 Auth E2E는 Envoy로 JWT/JWKS와 Session 앞단을 검증한다. 실제 Istio Ingress와 Kubernetes 정책 객체는 별도 환경에서 검증해야 한다.

## 실패 시 점검 포인트

| 증상 | 점검 |
| --- | --- |
| Docker build 실패 | Docker Desktop/Engine 실행 상태 확인 |
| `docker compose` 실패 | Docker Compose plugin 설치 여부 확인 |
| pytest import 실패 | `task test-unit`로 Docker 테스트 러너를 통해 실행했는지 확인 |
| DB 연결 실패 | `DATABASE_URL` 값과 PostgreSQL 실행 상태 확인 |
| Kafka 이벤트 검증 실패 | Compose `kafka:29092`, topic 생성, consumer 로그 확인 |
| Observability smoke 실패 | `docker compose -p dropmong-observability-e2e -f tests/e2e/observability/docker-compose.yml logs otel-collector tempo go-reference-service` 확인 |
| Newman 401 | access token의 서명·만료와 Session 폐기 여부 확인 |
| Newman 403 | customer/operator/admin 권한 헤더와 요청 데이터의 권한 관계 확인 |
| Newman 404 | 서비스 URL과 API path 확인 |
| Compose healthcheck timeout | `docker compose -p dropmong-e2e -f tests/e2e/docker-compose.yml ps`와 각 서비스 로그 확인 |
