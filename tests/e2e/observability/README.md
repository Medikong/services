# Observability E2E

이 폴더는 Kubernetes 없이 trace 수집 경로를 검증하는 로컬 Docker Compose 구성이다. 기존 `tests/e2e/docker-compose.yml`의 서비스 기능 E2E와 목적을 섞지 않는다.

## 검증 범위

```text
go-reference-service OpenTelemetry instrumentation
-> OTLP gRPC
-> OpenTelemetry Collector OTLP receiver
-> Tempo
-> Tempo HTTP API polling
```

Grafana는 Tempo datasource provisioning과 수동 조회 보조 역할만 맡는다. 자동 성공/실패 판정은 `scripts/trace-smoke.py`가 Tempo API를 polling해 결정한다.

## 실행

루트에서 실행한다.

```bash
task tests:test-observability-e2e
```

수동 확인이 필요하면 stack만 올릴 수 있다.

```bash
task tests:observability-e2e-up
task tests:observability-e2e-smoke
task tests:observability-e2e-down
```

기본 포트는 다음과 같다.

| 대상 | URL |
| --- | --- |
| Tempo | `http://localhost:3200` |
| Grafana | `http://localhost:3001` |
| OpenTelemetry Collector OTLP gRPC | `localhost:4317` |
| OpenTelemetry Collector OTLP HTTP | `localhost:4318` |

포트 충돌이 있으면 Compose 환경변수로 바꾼다.

```bash
GRAFANA_PORT=13001 TEMPO_PORT=13200 task tests:test-observability-e2e
```

## Smoke 기준

1. `go-reference-service`의 admin 포트에서 `/healthz`, `/readyz`가 응답하는지 확인한다. 이 공용 endpoint와 `/metrics`는 trace 제외 기본값이다.
2. Tempo와 Collector readiness endpoint가 응답하는지 확인한다.
3. 고유 `X-Request-Id`를 붙여 `/healthz`, `/readyz`, `/metrics`를 호출하고 Tempo에 trace가 생기지 않는지 확인한다.
4. 고유 `X-Request-Id`, principal, idempotency key를 붙여 `go-reference-service`의 `POST /v1/reference/resources/{resourceID}/audit`를 호출한다.
5. Tempo `/api/search`에서 `service.name=go-reference-service request_id=<id>` 조건으로 trace를 찾는다.
6. trace 상세에서 `service.name`, `request_id`, span name, trace id가 모두 존재하는지 확인한다.

## Trace 제외 endpoint

공통 observability 설정은 기본적으로 다음 operational endpoint를 trace에서 제외한다.

```text
/healthz,/readyz,/metrics
```

Go middleware가 이 endpoint들을 span 생성 전에 제외한다. 운영 probe와 Prometheus scrape는 그대로 응답하지만 Tempo에는 업무 요청 중심의 span만 남긴다.

## 제외 범위

- Prometheus metric scrape 경로 검증
- stdout JSON 로그 수집과 Loki 저장
- 감사 로그 저장 또는 trace backend 저장
- Kong/Ingress/Gateway trace boundary
- 메시징 publish/consume trace 전파의 전체 E2E

메시징 흐름이 추가되면 각 서비스의 producer/consumer wiring을 기준으로 별도 확장한다. 이 폴더는 우선 Go inbound request span이 실제 Collector/Tempo 경로로 들어가는지 보장한다.
