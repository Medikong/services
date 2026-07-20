# Auth Redis Observability E2E

이 폴더는 auth-service의 Redis 관측성과 세션 폐기 projection 재시도를 실제 Docker 컨테이너로 검증한다. 기능 E2E와 별도로 PostgreSQL, Valkey, auth worker, OpenTelemetry Collector, Tempo, Prometheus, redis_exporter, Loki, Alloy, Grafana를 올리고, 자동 판정 스크립트가 cache miss, cache hit, Redis 장애·복구와 폐기 tombstone 전달을 확인한다.

Grafana는 사람이 결과를 살펴보는 도구다. 테스트의 성공 여부는 Tempo, Prometheus, Loki API를 조회하는 smoke 스크립트가 결정한다.

## 먼저 알아둘 말

- **Trace**는 한 요청의 이동 기록이다. 택배 조회 화면에 접수, 이동, 도착이 차례로 찍히듯이, 요청이 HTTP에서 Redis와 PostgreSQL을 거친 기록을 한곳에서 보여준다.
- **Cache hit**는 공책에 적어 둔 답을 바로 찾은 경우다. **Cache miss**는 공책에 답이 없어 PostgreSQL에 물어본 뒤, 다음에 바로 찾도록 Redis에 적어 두는 경우다.
- **폐기 projection**은 PostgreSQL에 적힌 Session 폐기 사실을 Redis에도 전달하는 작업이다. Redis가 잠시 꺼져 있으면 worker가 PostgreSQL 작업을 남겨 두었다가 복구 후 다시 전달한다.
- 활성 cache는 최대 `5m`, 폐기 tombstone은 최대 `20m`으로 고정한다. Session의 남은 유효시간이 더 짧으면 tombstone도 그 시간까지만 유지하며, 늦게 도착한 활성 cache 쓰기가 폐기 사실을 덮지 못하게 막는다.

## 준비 사항

저장소 루트에서 실행한다. Docker Desktop이 실행 중이어야 하며, 이 작업의 기본 Docker context는 `desktop-linux`다.

```bash
docker context show
docker info >/dev/null
```

## 한 번에 실행

다음 명령 하나가 stack 준비, fixture 생성, miss·hit·장애 검증, worker 재시도, Redis 복구 확인, 자원 정리를 모두 수행한다.

```bash
task tests:test-auth-observability-e2e
```

성공하거나 실패해도 이 명령은 전용 Compose project의 컨테이너와 volume, 런타임 RSA 테스트 키를 정리한다. 실패 메시지는 문제가 생긴 backend와 판정 조건을 알려주며 JWT, 비밀번호, 쿠키, 세션 Redis key·value는 출력하지 않는다.

## Stack을 유지하며 확인

Grafana나 backend API를 직접 살펴보려면 stack을 올린 뒤 smoke를 따로 실행한다.

```bash
task tests:auth-observability-e2e-up
task tests:auth-observability-e2e-smoke
```

`up`은 수동 확인을 위해 컨테이너와 volume을 유지한다. 확인을 마치면 반드시 다음 명령으로 종료한다.

```bash
task tests:auth-observability-e2e-down
```

`down`은 전용 Compose 자원과 stack 유지에 사용한 런타임 RSA 테스트 키를 정리한다.

## 포트 변경

기본 포트가 다른 프로그램과 겹치면 다음 환경변수로 host 포트를 바꿀 수 있다.

| 환경변수 | 대상 |
| --- | --- |
| `AUTH_OBS_AUTH_PORT` | auth-service public HTTP |
| `AUTH_OBS_ADMIN_PORT` | auth-service admin HTTP와 `/healthz`, `/readyz`, `/metrics` |
| `AUTH_OBS_TEMPO_PORT` | Tempo HTTP API |
| `AUTH_OBS_PROMETHEUS_PORT` | Prometheus UI와 HTTP API |
| `AUTH_OBS_LOKI_PORT` | Loki HTTP API |
| `AUTH_OBS_GRAFANA_PORT` | Grafana UI |
| `AUTH_OBS_COLLECTOR_GRPC_PORT` | OTLP gRPC 수신 포트 |
| `AUTH_OBS_COLLECTOR_HTTP_PORT` | OTLP HTTP 수신 포트 |
| `AUTH_OBS_COLLECTOR_HEALTH_PORT` | OTel Collector health API |
| `AUTH_OBS_ALLOY_PORT` | Alloy health API |

예를 들어 현재 shell에서 포트를 고정한 뒤 stack을 유지하려면 다음처럼 실행한다.

```bash
export AUTH_OBS_AUTH_PORT=18089
export AUTH_OBS_ADMIN_PORT=19090
export AUTH_OBS_TEMPO_PORT=13200
export AUTH_OBS_PROMETHEUS_PORT=19091
export AUTH_OBS_LOKI_PORT=13100
export AUTH_OBS_GRAFANA_PORT=13001

task tests:auth-observability-e2e-up
task tests:auth-observability-e2e-smoke
task tests:auth-observability-e2e-down
```

같은 변수를 `task tests:test-auth-observability-e2e` 앞에 지정하면 일괄 실행에도 적용된다. `up`, `smoke`, `down`을 나눠 실행할 때는 세 명령이 같은 환경변수 값을 사용해야 한다.

## 자동 판정 범위

Smoke 스크립트는 다음 조건을 자동으로 확인한다.

1. 실제 Auth intent와 이메일 로그인을 사용해 활성 세션과 유효한 access JWT를 만든다. 사용자 fixture만 PostgreSQL에 넣으며 운영 API에 테스트 우회 경로를 추가하지 않는다.
2. cache miss 요청에서 Redis GET miss, PostgreSQL 조회, Redis write-through가 같은 trace에 있고 응답이 성공하는지 확인한다.
3. cache hit 요청에서 Redis GET이 성공하고 같은 trace에 PostgreSQL 조회가 없는지 확인한다.
4. Redis 중단 시 `/internal/session/status`가 fail-closed 응답을 반환하고 Redis error span과 `/readyz` 실패가 생기는지 확인한다.
5. Redis 중단 중 PostgreSQL에서 Session을 폐기한 뒤 projection 작업이 `pending|processing`이고 재시도 횟수가 증가했는지 비민감 boolean 조회로 확인한다.
6. Redis를 다시 시작한 뒤 worker가 작업을 `delivered`로 마치고, 같은 access JWT의 `/internal/session/status` 요청이 `401 AUTH_SESSION_REVOKED`를 반환하는지 확인한다.
7. 응답 `X-Trace-Id`, Tempo trace ID, `service.name=auth-service`, 고유 `request_id`, HTTP parent-child 관계를 대조한다.
8. `/healthz`, `/readyz`, `/metrics`가 trace에서 제외되는지 확인한다.
9. auth-service Redis client metric, `redis_up`, 명령 처리량, keyspace hit·miss 변화가 Prometheus에 들어오는지 확인한다.
10. 같은 `request_id`와 `trace_id`, route, status를 가진 JSON access log를 Loki에서 찾는다.
11. 수집된 trace와 log, 실패 출력에 테스트 인증정보나 세션 Redis key·value가 없는지 검사한다.

redis_exporter 수치는 stack 전체의 Redis 활동을 나타내므로 readiness와 exporter 자체 요청의 영향도 받을 수 있다. 자동 판정은 시나리오 직전 기준값과 이후 증가량을 비교하고, 특정 요청이 miss인지 hit인지는 Tempo span 구성으로 함께 확인한다.

## Grafana Explore

Grafana는 `http://127.0.0.1:<AUTH_OBS_GRAFANA_PORT>`에서 연다. 포트를 지정하지 않았다면 `docker-compose.yml`에 선언된 기본값을 사용한다. Tempo, Loki, Prometheus datasource는 미리 provision된다.

### Tempo에서 Trace 찾기

1. 왼쪽 메뉴에서 **Explore**를 열고 datasource로 **Tempo**를 선택한다.
2. smoke 결과의 `X-Trace-Id`를 Trace ID 입력란에 붙여 넣는다.
3. HTTP server span 아래에 Redis span이 있고, miss일 때만 PostgreSQL과 Redis write-through span이 추가되는지 확인한다.
4. Search나 TraceQL을 사용한다면 다음 조건으로 auth-service trace를 좁힐 수 있다.

```traceql
{ resource.service.name = "auth-service" }
```

고유 request ID를 알고 있다면 다음처럼 찾을 수 있다.

```traceql
{ resource.service.name = "auth-service" && span.request_id = "<request-id>" }
```

### Loki에서 같은 요청의 Log 찾기

Explore에서 datasource를 **Loki**로 바꾸고 `request_id` 또는 `trace_id`로 JSON access log를 찾는다.

```logql
{service="auth-service"} | json | request_id="<request-id>"
```

```logql
{service="auth-service"} | json | trace_id="<trace-id>"
```

결과에서 `http.route`, `http.status_code`, `request_id`, `trace_id`가 자동 판정 결과와 같은지 확인한다. 원본 log 전체를 공유하기 전에 인증정보가 없는지 다시 확인한다.

### Prometheus에서 Metric 확인하기

Explore에서 datasource를 **Prometheus**로 선택한다. 다음 query로 scrape 상태와 Redis 서버 변화를 볼 수 있다.

```promql
up{job="auth-service"}
```

```promql
redis_up
```

```promql
increase(redis_commands_processed_total[5m])
```

```promql
increase(redis_keyspace_hits_total[5m])
```

```promql
increase(redis_keyspace_misses_total[5m])
```

auth-service의 Redis OpenTelemetry client metric은 `db_client_connections_use_time_milliseconds_count`다. `job="auth-service"`, `db_system="redis"`, `type="command|pipeline"`, `status="nil|ok|error"` 라벨을 조합하면 miss, 정상 명령, 장애, write-through를 구분할 수 있다. 자동 smoke도 실제 `/metrics`에서 이 metric family를 확인한 뒤 같은 이름으로 판정한다.

## 민감정보 주의

- `curl -v`, 전체 HTTP header 출력, 로그인 응답 전체 출력은 사용하지 않는다.
- JWT, refresh token, 비밀번호, 쿠키, fixture email, 세션 ID, Redis key·value를 실패 메시지나 문서에 붙이지 않는다.
- projection 작업 판정은 개수나 참·거짓만 읽고 `session_id`, `user_id`, token, Redis 원문을 stdout이나 실패 메시지로 출력하지 않는다.
- Tempo·Loki API 원본 응답을 그대로 공유하지 않는다. 필요한 trace ID, request ID, route, status와 판정 조건만 남긴다.
- 민감정보 검사는 이 E2E에서 사용하는 알려진 테스트 값과 금지 패턴을 대상으로 한다. 모든 운영 데이터의 비노출을 일반적으로 증명하는 보안 감사는 아니다.

## 범위와 제약

- 로컬 Docker Compose에서 auth-service의 Redis client, Redis 서버, trace, metric, log 상관관계만 검증한다.
- Istio, Kubernetes, Kong, Kafka tracing, profiling, 다른 서비스는 포함하지 않는다.
- Grafana 화면은 수동 확인용이며 자동 성공 조건에 포함되지 않는다.
- Tempo, Prometheus, Loki 반영에는 짧은 지연이 있을 수 있어 smoke가 제한 시간 안에서 반복 조회한다.
- `up`으로 유지한 stack에는 로컬 테스트 데이터가 남고 시스템 자원을 사용한다. 확인이 끝나면 `task tests:auth-observability-e2e-down`을 실행한다.
