# Trace 설계 기준

이 문서는 `service` repo의 Python/FastAPI 서비스에서 OpenTelemetry trace를 어떤 기준으로 적용할지 정리한다.

목표는 모든 함수 호출을 trace로 나열하는 것이 아니다. 운영자가 장애와 지연을 분석할 때 요청, 에러, Kafka 이벤트, 로그를 같은 흐름 안에서 찾을 수 있도록 공통 경계를 먼저 계측한다.

## 결론

Python/FastAPI 서비스에서는 Go처럼 모든 함수가 error를 반환하고 `Wrap(err)`로 정보를 누적하는 방식을 강제하기 어렵다. Python의 예외 전파는 함수 선언에 드러나지 않고, 개발자별 규율에 크게 의존한다.

따라서 trace v1은 자동화 가능한 경계부터 적용한다.

- FastAPI inbound 요청은 공통 instrumentation으로 span을 자동 생성한다.
- 비즈니스 서비스 레이어는 OpenTelemetry를 직접 import하지 않는다.
- 예외 컨텍스트는 특정 `AppError` 클래스 강제가 아니라 독립 `packages/errors`의 context contract로 전파한다.
- 공통 exception handler가 current span과 structured log에 에러 정보를 한 번만 기록한다.
- 관측성 코드는 `packages/server`가 아니라 별도 `packages/observability`로 분리한다.
- `packages/errors`는 OpenTelemetry, FastAPI, structlog, Sentry에 의존하지 않는다.
- Kafka와 HTTP 전파는 payload가 아니라 protocol header에서 처리한다.
- dashboard가 여러 서비스를 직접 호출하는 사용자 행동 연결은 `client_action_id` 같은 별도 correlation key로 먼저 설계한다.

## 개념 구분

Trace와 stack trace는 다른 개념이다.

```text
trace
  - 여러 서비스와 비동기 처리에 걸친 요청 흐름
  - trace_id, span_id, parent/span link로 구성
  - Tempo/Grafana에서 서비스 간 흐름과 지연을 본다

stack trace
  - 한 프로세스 안에서 예외가 발생한 코드 경로
  - Python traceback 또는 exception chain으로 보존
  - 로그와 span exception event로 기록한다

request_id
  - HTTP 요청 단위 correlation id
  - 외부 응답, 로그 조회, 고객 문의 대응에 사용한다

business id
  - reservation_id, payment_id, ticket_id, event_id 같은 업무 식별자
  - trace 전파 수단이 아니라 검색과 업무 상태 대조용 attribute다
```

## 전체 서비스 아키텍처

Trace 설계는 서비스 내부 코드만이 아니라 외부 진입점, dashboard 호출 방식, Kafka 이벤트 흐름을 함께 본다. API Gateway 역할은 Kong Ingress를 우선 활용하고, 사용자 행동 단위 orchestration이 커지면 BFF를 둘 수 있다.

### BFF 없는 현재 구조

현재 구조에서는 dashboard가 Kong Ingress를 통해 각 FastAPI 서비스를 직접 호출한다. Kong은 edge 요청 단위 trace를 만들고 upstream 서비스로 trace context를 전달할 수 있지만, dashboard의 여러 API 호출을 하나의 사용자 행동으로 자동 묶지는 않는다.

```mermaid
flowchart LR
    subgraph Clients["clients"]
        User["사용자"]
        Provider["공연 운영자"]
        Admin["플랫폼 운영자"]
        Dashboard["dashboard"]
    end

    subgraph Edge["edge"]
        Kong["Kong Ingress<br/>API Gateway"]
    end

    subgraph App["application layer"]
        Auth["auth-service"]
        Concert["concert-service"]
        Reservation["reservation-service"]
        Payment["payment-service"]
        Ticket["ticket-service"]
        Notification["notification-service"]
    end

    subgraph Async["async/event layer"]
        Kafka["Kafka topics"]
        S3["S3<br/>QR/PDF"]
    end

    subgraph Observability["observability"]
        Collector["OpenTelemetry Collector"]
        Tempo["Tempo"]
        Loki["Loki"]
        Grafana["Grafana"]
    end

    User --> Dashboard
    Provider --> Dashboard
    Admin --> Dashboard
    Dashboard --> Kong

    Kong --> Auth
    Kong --> Concert
    Kong --> Reservation
    Kong --> Payment
    Kong --> Ticket
    Kong --> Notification

    Reservation -->|reservation-created / reservation-expired| Kafka
    Payment -->|payment-approved / payment-failed| Kafka
    Concert -->|concert-approved / sale-policy-updated| Kafka
    Ticket -->|ticket-issued| Kafka
    Kafka -->|payment-approved| Ticket
    Kafka -->|payment-failed| Reservation
    Kafka -->|reservation / payment / ticket events| Notification
    Ticket --> S3

    Kong --> Collector
    Auth --> Collector
    Concert --> Collector
    Reservation --> Collector
    Payment --> Collector
    Ticket --> Collector
    Notification --> Collector
    Collector --> Tempo
    Collector -.->|stdout JSON logs<br/>filelog path| Loki
    Tempo --> Grafana
    Loki --> Grafana
```

### BFF 적용 구조

BFF를 도입하면 dashboard는 Kong을 통해 `dashboard-bff`를 호출하고, BFF가 화면 또는 사용자 행동 단위 API를 제공한다. BFF는 여러 서비스 호출을 조합하면서 backend trace root와 `client_action_id`를 일관되게 관리할 수 있다.

```mermaid
flowchart LR
    subgraph Clients["clients"]
        User["사용자"]
        Provider["공연 운영자"]
        Admin["플랫폼 운영자"]
        Dashboard["dashboard"]
    end

    subgraph Edge["edge"]
        Kong["Kong Ingress<br/>API Gateway"]
    end

    subgraph App["application layer"]
        BFF["dashboard-bff<br/>user action orchestration"]
        Auth["auth-service"]
        Concert["concert-service"]
        Reservation["reservation-service"]
        Payment["payment-service"]
        Ticket["ticket-service"]
        Notification["notification-service"]
    end

    subgraph Async["async/event layer"]
        Kafka["Kafka topics"]
        S3["S3<br/>QR/PDF"]
    end

    subgraph Observability["observability"]
        Collector["OpenTelemetry Collector"]
        Tempo["Tempo"]
        Loki["Loki"]
        Grafana["Grafana"]
    end

    User --> Dashboard
    Provider --> Dashboard
    Admin --> Dashboard
    Dashboard --> Kong
    Kong --> BFF

    BFF --> Auth
    BFF --> Concert
    BFF --> Reservation
    BFF --> Payment
    BFF --> Ticket
    BFF --> Notification

    Reservation -->|reservation-created / reservation-expired| Kafka
    Payment -->|payment-approved / payment-failed| Kafka
    Concert -->|concert-approved / sale-policy-updated| Kafka
    Ticket -->|ticket-issued| Kafka
    Kafka -->|payment-approved| Ticket
    Kafka -->|payment-failed| Reservation
    Kafka -->|reservation / payment / ticket events| Notification
    Ticket --> S3

    Kong --> Collector
    BFF --> Collector
    Auth --> Collector
    Concert --> Collector
    Reservation --> Collector
    Payment --> Collector
    Ticket --> Collector
    Notification --> Collector
    Collector --> Tempo
    Collector -.->|stdout JSON logs<br/>filelog path| Loki
    Tempo --> Grafana
    Loki --> Grafana
```

BFF 없는 구조에서는 백엔드 FastAPI 계측만으로 “좌석 선택 -> 예약 -> 결제 -> 티켓 발급” 전체가 자동으로 하나의 trace가 되지 않는다. dashboard가 각 서비스를 독립 HTTP 요청으로 호출하기 때문이다.

따라서 v1에서는 각 서비스 요청을 안정적으로 trace/log와 연결하고, 사용자 행동 전체 correlation은 별도 설계로 둔다.

## 적용 원칙

### 1. boundary-first

trace는 개발자가 모든 함수에 수동으로 심는 기능이 아니다. 공통 경계에서 자동으로 수집한다.

우선 적용 경계는 다음과 같다.

- FastAPI request boundary
- common exception handler
- Kafka producer/consumer adapter
- HTTP client adapter 또는 instrumentation
- DB driver instrumentation

비즈니스 서비스 레이어는 trace를 모른다.

```text
허용
  packages/observability/src/observability/config.py
  packages/observability/src/observability/fastapi.py
  packages/observability/src/observability/logging.py
  packages/observability/src/observability/tracing.py
  packages/observability/src/observability/propagation.py
  packages/errors/src/errors/context.py
  packages/errors/src/errors/builder.py
  packages/errors/src/errors/exceptions.py
  services/*/app/main.py
  services/*/app/observability.py
  services/*/app/kafka.py
  services/*/app/consumers/*.py
  services/*/app/exceptions.py

비허용
  services/*/app/services/*.py 에서 opentelemetry 직접 import
  services/*/app/repositories/*.py 에서 trace_id 직접 전달
  request/response body에 trace_id 필드 추가
```

### 2. attribute-first

새 span을 만들기 전에 현재 span에 의미 있는 attribute를 붙이는 것을 우선한다.

좋은 attribute 후보:

- `app.use_case`
- `error.code`
- `error.domain`
- `event.type`
- `event.topic`
- `reservation.id`
- `payment.id`
- `ticket.id`
- `client_action_id`

피해야 할 attribute:

- raw JWT
- email, phone, address
- payment token
- large payload
- high-cardinality 값을 metric label 또는 Loki label로 승격하는 것

### 3. exception-context-first

Python에서는 Go처럼 error wrapping을 모든 함수에 강제하지 않는다. 대신 `samber/oops`의 builder/context 모델을 참고해 기존 예외에 context를 누적할 수 있는 독립 패키지를 둔다.

이 패키지는 에러 핸들링 프레임워크가 아니다. 예외에 대한 컨텍스트를 전파하는 도구다. 특정 base class 이름을 강제하지 않고, 공통 handler가 읽을 수 있는 context shape만 제공한다.

```text
exception context contract
  - code: 사람이 검색할 수 있는 안정적인 에러 코드
  - domain: auth, concert, reservation, payment, ticket, notification
  - message: 운영 로그에 쓸 내부 메시지
  - public_message: 외부 응답에 노출 가능한 메시지
  - tags: feature, dependency, retry 같은 분류 태그
  - attributes: 안전한 key/value 부가 정보
  - hint: runbook 또는 빠른 디버깅 힌트
  - owner: 책임 팀 또는 담당 영역
  - user: 안전하게 기록 가능한 사용자 식별자
  - tenant: tenant 또는 provider 식별자
  - occurred_at: 에러 발생 시각
  - duration_ms: 실패 전까지 걸린 시간
  - cause: Python exception chain으로 연결된 원인 예외
```

`oops`의 `Trace()`와 `Span()`은 error object가 correlation id를 담는 수단이다. Medikong에서는 OpenTelemetry의 distributed `trace_id`/`span_id`를 error builder가 새로 만들지 않는다. 그 값은 FastAPI/Kong/Kafka context에서 생성하고, exception handler가 current span에서 읽어 error context와 log에 붙인다.

Python 쪽 API는 다음 방향을 기준으로 한다. 핵심은 새 예외로 계속 감싸는 것이 아니라, 기존 예외에 context/tag/code/hint를 붙이고 같은 예외를 다시 raise하는 것이다.

```text
try:
  commit()
except IntegrityError as exc:
  in_domain("reservation")
    .code("reservation.conflict")
    .tag("seat")
    .with_attr("seat_id", seat_id)
    .public("Seat is already reserved.")
    .hint("Check active reservation unique constraint.")
    .attach(exc)
  raise
```

도메인 의미가 바뀌는 경계에서만 custom exception과 `raise ... from exc`를 사용한다.

```text
try:
  reserve_seat()
except IntegrityError as exc:
  in_domain("reservation").code("reservation.conflict").attach(exc)
  raise ReservationConflict("Seat is already reserved.") from exc
```

서비스 레이어가 `packages/errors`의 builder를 사용할 수는 있지만, OpenTelemetry API를 직접 호출하지 않는다. builder 사용도 모든 함수에 강제하지 않고, 운영상 검색 가능한 에러 코드와 안전한 metadata가 필요한 지점부터 적용한다.

경계면 handler는 이 정보를 한 번만 처리한다.

- HTTP error response 생성
- structured JSON log 작성
- Sentry event context/tag/fingerprint 구성
- current span에 exception event 기록
- span status를 ERROR로 설정
- `error.code`, `error.domain` attribute 기록

로컬 또는 CLI 실행에서는 같은 exception context를 `rich` 또는 `stackprinter`로 사람이 읽기 좋게 출력할 수 있다. 이 출력 어댑터도 `packages/errors` core가 아니라 별도 adapter로 둔다.

비즈니스 함수는 OpenTelemetry, structlog, Sentry API를 직접 호출하지 않는다.

### 4. propagation은 header에서 처리

trace context는 업무 payload에 넣지 않는다.

HTTP:

```text
traceparent
tracestate
```

Kafka:

```text
message headers:
  traceparent
  tracestate
  correlation_id
  causation_id
  client_action_id
```

업무 payload에는 기존 계약에 필요한 `eventId`, `correlationId`, `reservationId`, `paymentId` 같은 값을 유지한다. trace 전파는 adapter가 처리한다.

## v1 구현 범위

`service#14`의 기준 범위는 trace 기반 연동이다.

포함한다.

- `packages/observability`에서 FastAPI instrumentation 적용
- `service.name`, `service.version`, `deployment.environment` resource 설정
- `OTEL_TRACES_EXPORTER=otlp`와 OTLP endpoint가 있을 때 Collector 전송 활성화
- `OTEL_TRACES_EXPORTER=none` 또는 unsupported 값일 때 trace export 비활성화
- JSON request log에 `trace_id`, `span_id`, `request_id` 기록
- 대표 FastAPI 요청 1건에서 span 생성 smoke 확인

포함하지 않는다.

- dashboard `traceparent` 생성/전파
- API별 custom span 설계
- Kafka producer/consumer trace propagation
- 서비스 간 HTTP client propagation
- 업무 flow별 attribute 표준
- 서비스 코드 전반의 `packages/errors` exception context builder 적용
- Sentry/rich/stackprinter adapter
- 감사 로그 저장소 설계
- metric/log Collector 배포

## 후속 설계 범위

### dashboard action correlation

dashboard가 여러 서비스를 직접 호출하므로 사용자 행동 전체를 묶으려면 backend trace만으로는 부족하다.

v1 이후에는 dashboard가 사용자 행동마다 `client_action_id`를 만들고 관련 API 호출 header에 싣는 방식을 검토한다.

```text
X-Client-Action-Id: act-...
```

서비스는 이 값을 다음 위치에 기록한다.

- current span attribute
- JSON log field
- Kafka message header
- 필요한 event payload의 correlation field

브라우저에서 직접 `traceparent`를 만드는 방식은 프론트엔드 OpenTelemetry/RUM, sampling, untrusted context 정책을 함께 정한 뒤 적용한다.

### Kafka trace propagation

Kafka producer/consumer는 공통 adapter에서만 trace context를 다룬다.

Producer 책임:

- 현재 context를 Kafka header에 inject
- `event_id`, `correlation_id`, `client_action_id` header를 함께 전달
- publish 실패 시 current span과 log에 topic/event id 기록

Consumer 책임:

- Kafka header에서 context extract
- 짧은 후속 처리면 producer context를 parent로 이어받는다
- fan-out 또는 장시간 처리면 span link와 business id를 사용한다
- 처리 실패는 consumer span과 structured log에 한 번만 기록한다

현재 repo는 `aiokafka`를 사용하므로 `opentelemetry-instrumentation-aiokafka`를 우선 후보로 검토한다. 자동 계측이 부족하면 `packages/observability`에 Kafka header inject/extract helper를 둔다.

### Kong Ingress trace boundary

외부 진입점의 API Gateway 역할은 기존 Kong Ingress를 우선 활용한다. Kong은 사용자 행동 전체를 이해하는 애플리케이션 계층이 아니라, edge 요청을 받고 upstream 서비스로 전달하는 gateway 계층이다.

Kong이 담당할 수 있는 범위:

- client -> Kong -> FastAPI service 요청 단위 trace 생성
- `traceparent`, `tracestate` 같은 trace context upstream 전파
- gateway latency와 upstream latency 분리
- request id 또는 correlation id header 보장
- Kong access log와 application log를 같은 request id 또는 trace id로 조회할 수 있게 연결
- Kong span을 OTLP로 Collector에 전송

Kong만으로 해결하지 않는 범위:

- dashboard의 여러 API 호출을 하나의 사용자 행동으로 자동 연결
- 예약 -> 결제 -> 티켓 발급 같은 화면/유스케이스 orchestration
- Kafka consume 이후 비동기 처리의 업무적 원인 관계 해석
- 서비스 내부 도메인 에러 metadata 표준화

따라서 Kong trace는 다음 구조를 목표로 한다.

```text
dashboard
  -> Kong Ingress span
    -> FastAPI inbound span
      -> request log trace_id/request_id
```

사용자 행동 단위 correlation은 Kong이 아니라 dashboard의 `client_action_id` 또는 미래의 BFF가 담당한다.

### HTTP client propagation

서비스 간 HTTP 호출이 생기면 각 서비스가 직접 header를 조립하지 않는다.

우선순위:

- 공통 HTTP client wrapper
- `opentelemetry-instrumentation-httpx`
- `opentelemetry-instrumentation-requests`

선택 기준은 실제 사용하는 client library를 따른다.

## custom span 허용 기준

기본값은 custom span을 만들지 않는 것이다.

허용되는 경우:

- 운영자가 Tempo에서 봤을 때 병목/실패 지점을 바로 이해할 수 있는 단계
- 외부 시스템 호출 또는 비동기 경계
- 하나의 request span 안에서 시간이 오래 걸리는 주요 단계
- 장애 때 API path만으로 원인을 구분하기 어려운 단계

예시:

```text
reservation.reserve_seat
payment.authorize
ticket.issue
kafka.publish.payment_approved
kafka.consume.payment_approved
```

금지되는 경우:

- 단순 getter/setter
- repository 내부 private helper
- 모든 service method에 일괄 decorator 적용
- loop 안에서 item마다 span 생성
- PII나 큰 payload를 attribute로 기록

## 로그 연결 기준

모든 application log는 stdout/stderr JSON으로 남긴다. 로그 수집은 Collector filelog 또는 Alloy/Loki 경로에서 처리하고, 애플리케이션은 OTLP logs exporter를 켜지 않는다.

기본 로그 필드:

```text
timestamp
severity
severity_text
service.name
request_id
trace_id
span_id
http.method
http.route
http.status_code
duration_ms
```

에러 로그 추가 필드:

```text
error.code
error.domain
error.type
error.message
exception.stacktrace
```

`trace_id`는 Grafana trace-to-logs 조회를 위한 structured field로 둔다. Loki label로 승격하지 않는다.

## 책임 경계

`packages/observability`가 소유한다.

- OpenTelemetry SDK 설정
- FastAPI instrumentation
- OTLP exporter 설정
- request_id/trace_id/span_id 로그 연결
- `packages/errors`의 exception context를 span/log/Sentry event에 반영하는 adapter
- Kafka/HTTP propagation helper

`packages/errors`가 소유한다.

- exception context object
- context/tag/code/hint/attribute builder
- 기존 예외에 context를 attach하고 같은 예외를 다시 raise할 수 있는 helper
- 도메인 의미가 바뀔 때 사용할 custom exception 작성 기준과 `raise from` 기준
- context extraction protocol

`packages/errors` core에 넣지 않는다.

- OpenTelemetry trace_id/span_id 생성 또는 current span 조회
- FastAPI exception handler
- structlog/Sentry/rich/stackprinter adapter
- HTTP request/downstream response metadata 수집

`packages/server`가 소유한다.

- `/healthz`, `/readyz`, `/metrics` 같은 운영 endpoint
- readiness check
- Prometheus runtime collector
- FastAPI 서버 운영 handler

개별 서비스가 소유한다.

- `setup_request_observability(app, settings.observability_config())` 호출
- 안전한 exception context code/domain/attributes 제공
- 업무 식별자를 event payload와 response에 기존 계약대로 유지
- 필요한 경우 공통 helper를 통한 boundary attribute 추가

`workspace`와 `gitops` repo가 소유한다.

- trace 정책 문서와 상위 ADR
- Collector, Tempo, Grafana datasource
- trace retention, sampling, dashboard, alert 기준

## 구현 순서

1. `packages/errors`를 만들고 `samber/oops`를 참고한 exception context builder와 extraction protocol을 설계한다.
2. `packages/observability`를 만들고 FastAPI inbound trace와 request log correlation을 고정한다.
3. 기존 `packages/server`의 관측성 코드는 `packages/observability`로 이동하고, 운영 endpoint 책임만 `packages/server`에 남긴다.
4. exception handler가 current span, JSON log, Sentry event에 exception context를 기록하도록 공통화한다.
5. 서비스별 error response shape와 request_id 사용 방식을 정렬한다.
6. Kafka producer/consumer adapter에서 header propagation을 추가한다.
7. dashboard `client_action_id` header 정책을 정한다.
8. 필요한 서비스 간 HTTP client propagation을 추가한다.
9. 운영상 필요한 custom span만 제한적으로 추가한다.

## 참고

- Google Dapper, https://research.google/pubs/dapper-a-large-scale-distributed-systems-tracing-infrastructure/
- Uber Jaeger, https://www.uber.com/dk/en/blog/distributed-tracing/
- Slack notification tracing, https://slack.engineering/tracing-notifications/
- OpenTelemetry FastAPI instrumentation, https://opentelemetry-python-contrib.readthedocs.io/en/latest/instrumentation/fastapi/fastapi.html
- OpenTelemetry aiokafka instrumentation, https://opentelemetry-python-contrib.readthedocs.io/en/latest/instrumentation/aiokafka/aiokafka.html
- Kong OpenTelemetry plugin, https://docs.konghq.com/hub/kong-inc/opentelemetry/
- Kong tracing reference, https://docs.konghq.com/gateway/latest/production/tracing/
- samber/oops, https://github.com/samber/oops
- structlog, https://www.structlog.org/en/stable/
- Sentry Python, https://docs.sentry.io/platforms/python/
- rich traceback, https://rich.readthedocs.io/en/stable/traceback.html
- stackprinter, https://github.com/cknd/stackprinter
- Honeycomb custom instrumentation, https://www.honeycomb.io/blog/span-or-attribute-opentelemetry-custom-instrumentation
- Grafana Tempo trace to logs, https://grafana.com/docs/grafana/latest/datasources/tempo/configure-tempo-data-source/configure-trace-to-logs/
