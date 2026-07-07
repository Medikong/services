# 운영 엔드포인트 공통 규칙

이 문서는 DropMong 서비스군이 공통으로 제공해야 하는 운영 엔드포인트 규칙을 정의한다.

모든 서비스는 기능 API와 별개로 다음 엔드포인트를 제공한다.

| Endpoint | 목적 | Kubernetes 용도 | 인증 |
| --- | --- | --- | --- |
| `GET /healthz` | 프로세스 생존 확인 | liveness probe | 없음 |
| `GET /readyz` | 트래픽 수신 준비 상태 확인 | readiness probe | 없음 |
| `GET /metrics` | Prometheus 지표 노출 | ServiceMonitor scrape | 없음 |

## `GET /healthz`

`/healthz`는 서비스 프로세스가 살아 있는지만 확인한다. DB, Kafka, Redis 같은 외부 의존성은 확인하지 않는다.

성공 응답은 `common/components.yaml#/components/schemas/HealthResponse`를 사용한다.

```json
{
  "status": "ok",
  "service": "auth-service",
  "timestamp": "2026-05-28T10:15:30Z"
}
```

규칙:

- 정상 상태는 `200 OK`를 반환한다.
- 외부 의존성 장애로 실패시키지 않는다.
- Kubernetes liveness probe에 사용한다.
- 실패가 반복되면 Kubernetes가 container를 재시작할 수 있다.

## `GET /readyz`

`/readyz`는 서비스가 실제 트래픽을 받을 준비가 되었는지 확인한다.

성공 응답은 `common/components.yaml#/components/schemas/ReadinessResponse`를 사용한다.

```json
{
  "status": "ready",
  "service": "auth-service",
  "checks": {
    "database": "ok"
  },
  "timestamp": "2026-05-28T10:15:30Z"
}
```

규칙:

- 준비 상태면 `200 OK`를 반환한다.
- 필수 의존성에 접근할 수 없으면 `503 Service Unavailable`을 반환한다.
- Kubernetes readiness probe에 사용한다.
- readiness 실패 pod에는 Service traffic이 전달되지 않아야 한다.

서비스별 필수 checks 예시는 다음과 같다.

| Service | Required checks |
| --- | --- |
| `auth-service` | `database` |
| `catalog-service` | `database` |
| `order-service` | `database`, `kafka` |
| `payment-service` | `database`, `kafka` |
| `notification-service` | `database`, `kafka` |

의존성이 아직 구현되지 않은 MVP 단계에서는 실제 연결이 없는 항목을 checks에 넣지 않는다. 구현된 필수 의존성만 확인한다.

## `GET /metrics`

`/metrics`는 Prometheus text exposition format으로 지표를 반환한다.

응답 content type은 Prometheus client가 제공하는 값을 따른다.

```text
text/plain; version=0.0.4; charset=utf-8
```

규칙:

- 정상 상태는 `200 OK`를 반환한다.
- 인증을 요구하지 않는다.
- 사람이 소비하는 JSON이 아니라 Prometheus scrape 대상이다.
- 공통 HTTP 지표는 모든 서비스가 같은 이름과 label을 사용한다.

권장 공통 지표:

| Metric | Type | Labels | 설명 |
| --- | --- | --- | --- |
| `http_requests_total` | Counter | `service`, `method`, `path`, `status` | HTTP 요청 수 |
| `http_request_duration_seconds` | Histogram | `service`, `method`, `path` | HTTP 요청 처리 시간 |
| `service_ready` | Gauge | `service` | 서비스 준비 상태. ready면 `1`, 아니면 `0` |

서비스별 비즈니스 지표는 각 서비스 문서에서 추가하되, 공통 HTTP 지표 이름은 바꾸지 않는다.

## OpenAPI 참조 방식

서비스별 OpenAPI에서 운영 엔드포인트 응답 schema는 common component를 참조한다.

```yaml
responses:
  "200":
    description: Service process is alive.
    content:
      application/json:
        schema:
          $ref: ../../common/components.yaml#/components/schemas/HealthResponse
```

서비스별 파일 위치에 따라 상대 경로는 달라질 수 있다.

## 구현 기준

FastAPI 서비스는 다음 기준으로 구현한다.

- `/healthz`: DB session을 열지 않는다.
- `/readyz`: 필수 의존성을 짧은 timeout으로 확인한다.
- `/metrics`: `prometheus_client.generate_latest()`를 반환한다.
- 운영 엔드포인트는 JWT 인증 대상에서 제외한다.
- 로그에는 `service`, `requestId`, `method`, `path`, `status`, `durationMs`를 남긴다.
