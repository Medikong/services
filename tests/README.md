# 테스트 실행 가이드

이 프로젝트의 테스트 진입점은 루트 `Taskfile.yml`이다. Go 공용 기반 구조와 reference service, Python 기반 catalog/order/payment/notification 구매 흐름을 함께 검증한다.

업무 흐름을 사람이 직접 검증하거나 장애를 주입해 확인하는 절차는 배포/인프라 repo에서 별도 문서로 관리한다.

## 테스트 범위

| 구분 | 도구 | 대상 |
| --- | --- | --- |
| Go 단위 테스트 | Go test | `packages/go-*`, `services/go-reference-service` |
| Go 통합 테스트 | Go test + `integration` build tag | reference service와 공용 패키지의 조립 경계 |
| Go 벤치마크 | Go test + `benchmark` build tag | Go handler, 공용 패키지, 핵심 경로 성능 |
| Python 단위 테스트 | Docker Python pytest 러너 | `catalog-service`, `order-service`, `payment-service`, `notification-service` |
| Purchase E2E | Docker Compose, PostgreSQL, Kafka, Docker Newman 컨테이너 | catalog/order/payment/notification 구매, 결제 실패, 품절/동시성 검증 |
| Observability E2E | Docker Compose, OpenTelemetry Collector, Tempo, Grafana, Python smoke 컨테이너 | Go inbound request span이 OTLP -> Collector -> Tempo 경로로 적재되는지 검증 |
| Gateway E2E | 별도 future/Kubernetes scope | Istio Gateway/JWT/Ingress 라우팅, gateway trace boundary 검증 |

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

## Purchase E2E

구매 흐름은 Newman 컬렉션으로 검증한다.

```bash
task tests:purchase-e2e
```

특정 시나리오만 실행할 때는 다음처럼 지정한다.

```bash
task tests:purchase-e2e SCENARIO=04-customer-drop-purchase-happy-path
task tests:purchase-e2e SCENARIO=05-payment-failure-flow
task tests:purchase-e2e SCENARIO=06-sold-out-concurrency-flow
```

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

C003 전체 회귀 확인에서는 notification unit 18개, `04/05/06/07`, `04/08`이 통과했다. `09` runner는 Go Task shell에 `grep`이 없어 시작 전에 실패했으므로, 전체 `04-09` 회귀가 모두 재통과했다고 판정하지 않는다. 이 제한을 해소하기 전까지 `09`는 알려진 runner 제한으로 기록한다.

Purchase E2E는 Compose 네트워크 DNS로 `catalog-service`, `order-service`, `payment-service`, `notification-service`를 직접 호출한다. 기본 URL은 다음과 같다.

| 서비스 | 기본 URL |
| --- | --- |
| `catalog-service` | `http://catalog-service:8081` |
| `order-service` | `http://order-service:8082` |
| `payment-service` | `http://payment-service:8083` |
| `notification-service` | `http://notification-service:8084` |

Python 구매 서비스는 현재 Gateway가 검증한 JWT claim에서 만들어진 `X-User-Id`, `X-User-Email`, `X-User-Role` 컨텍스트 헤더를 신뢰하는 구조로 테스트한다. 외부 클라이언트가 이 헤더를 직접 보내는 상황은 별도 Gateway E2E에서 차단 여부를 확인한다.

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

`.github/workflows/e2e.yml`은 Docker Compose 기반 E2E stack을 실행한다. Gateway/JWT/Ingress 검증은 기본 E2E와 분리하고, 이후 필요하면 `task test-gateway-e2e` 같은 별도 타깃에서 Ingress 주소, JWT 생성, Gateway 라우팅 검증을 다룬다.

## 실패 시 점검 포인트

| 증상 | 점검 |
| --- | --- |
| Docker build 실패 | Docker Desktop/Engine 실행 상태 확인 |
| `docker compose` 실패 | Docker Compose plugin 설치 여부 확인 |
| pytest import 실패 | `task test-unit`로 Docker 테스트 러너를 통해 실행했는지 확인 |
| DB 연결 실패 | `DATABASE_URL` 값과 PostgreSQL 실행 상태 확인 |
| Kafka 이벤트 검증 실패 | Compose `kafka:29092`, topic 생성, consumer 로그 확인 |
| Observability smoke 실패 | `docker compose -p dropmong-observability-e2e -f tests/e2e/observability/docker-compose.yml logs otel-collector tempo go-reference-service` 확인 |
| Newman 401 | Gateway E2E가 아닌지, 서비스가 요구하는 인증 헤더가 누락됐는지 확인 |
| Newman 403 | customer/operator/admin 권한 헤더와 요청 데이터의 권한 관계 확인 |
| Newman 404 | 서비스 URL과 API path 확인 |
| Compose healthcheck timeout | `docker compose -p dropmong-e2e -f tests/e2e/docker-compose.yml ps`와 각 서비스 로그 확인 |
