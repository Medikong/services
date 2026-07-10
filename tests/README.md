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
