# Go Reference Service

Python `services/reference-service`에서 검증한 요청 문맥, 구조화 로그, trace, profile, health, metric 정책을 Go로 옮긴 실행 가능한 기반 구조입니다. 비즈니스 기능을 미리 설계하지 않고 외부 연결과 서버 수명주기만 기준으로 삼습니다.

## 구성

```text
go-reference-service/
├── cmd/
│   ├── server/                 REST, 선택적 gRPC health, 운영 API
│   ├── worker/                 PostgreSQL 감사 outbox relay
│   └── migrate/                서비스·outbox 스키마 적용
├── internal/
│   ├── app/                    의존성 조립과 종료 순서
│   ├── domain/sample/          pgx transaction + fencing + outbox 최소 예제
│   ├── platform/
│   │   ├── config/             환경 변수 파싱과 검증
│   │   └── observability/      Prometheus, OTel metric, pprof, Pyroscope
│   └── transport/http/         Chi route와 route 전용 middleware
├── api/                        운영·관측 API 계약
├── .env.example
├── Dockerfile
└── go.mod
```

공통 정책은 다음 위치에 있습니다.

- `packages/go-platform`: `slog`, request context, `oops` HTTP 변환, `otelhttp`, pgx pool, Redis lock, health/readiness/drain, Kafka offset 처리
- `packages/go-audit`: 감사 envelope, PostgreSQL outbox, lease/retry/DLQ, goose migration, 최종 감사 테이블 보관
- `packages/go-authz`, `packages/go-contracts`: Principal과 공통 header 계약

## 원칙

- PostgreSQL은 `*pgxpool.Pool`과 `pgx.Tx`를 그대로 사용합니다. 별도 DB interface나 transaction manager를 만들지 않습니다.
- DB 동시 실행 상한은 `POSTGRES_POOL_MAX_CONNS`가 담당합니다. 모든 요청과 worker 작업은 deadline이 있는 context를 사용합니다. 고비용 쿼리만 별도로 격리해야 할 때 endpoint 또는 worker 경계에 semaphore를 추가합니다.
- Redis는 `*redis.Client`를 그대로 전달합니다. cache interface는 없습니다. `redisotel` hook만 연결합니다.
- HTTP middleware는 Chi 내부에서 `RequestContext -> Trace -> Metric -> AccessLog -> Recovery -> Handler` 순서로 실행됩니다. request/response body는 기본 로그 대상이 아니며 민감 log key는 `slog.ReplaceAttr`에서 마스킹합니다.
- lock middleware는 `bsm/redislock`을 사용하고 TTL의 1/3 간격으로 갱신합니다. 획득 실패는 `423`, 갱신 실패는 작업 context 취소로 처리합니다.
- fencing token은 Redis `INCR`로 발급하고 PostgreSQL write가 이전 token보다 큰 값만 허용합니다. Redis lock만으로 데이터 정합성을 보장한다고 가정하지 않습니다.
- 변경 API는 관측용 `X-Request-Id`와 별개로 `Idempotency-Key`를 필수로 받습니다. 클라이언트는 같은 리소스에 대한 재시도에서 이 값을 유지해야 합니다.
- 예제의 fencing counter는 단일 Redis 기준입니다. failover 후에도 엄격한 단조 증가가 필요하면 token을 선형화 가능한 저장소에서 발급합니다.
- 감사 이벤트는 비즈니스 변경과 같은 pgx transaction에서 outbox에 기록합니다. worker는 `FOR UPDATE SKIP LOCKED` lease, 지수 backoff, 최대 시도 후 `dead` 상태를 사용합니다. 최종 `audit_events` insert는 event ID 기준으로 멱등입니다.
- 감사 payload 생성 시 password, token, authorization, cookie, secret 계열 key는 기본 마스킹하며 서비스별 민감 key를 추가로 넘길 수 있습니다.
- worker는 한 번에 한 행만 선점하고 설정된 batch 수만큼 반복합니다. 느린 발송 때문에 뒤쪽 행의 lease가 먼저 만료되는 문제를 피합니다.
- `AUDIT_LEASE`는 `AUDIT_PUBLISH_TIMEOUT`의 두 배 이상이어야 합니다. 잘못된 값은 worker 시작 전에 거절합니다.
- sink 발송 함수는 전달받은 context 취소를 지켜야 합니다. Go는 취소를 무시하고 멈춘 함수를 강제로 종료할 수 없으므로 worker는 종료 제한 시간이 지나면 DB를 실행 중 goroutine 아래에서 닫지 않고 프로세스 종료로 넘깁니다.
- `AUDIT_SINK_DATABASE_URL`을 비우면 같은 DB에 보관하고, 지정하면 별도 PostgreSQL의 `audit_events`로 전달합니다. source outbox와 sink insert는 분리되어 있으므로 sink 쓰기는 event ID로 멱등 처리합니다.
- migration은 embedded SQL과 goose version table을 사용하며 전체 실행 시간을 `MIGRATION_TIMEOUT`으로 제한합니다. server와 worker는 schema version이 최신이 아니면 시작하지 않고 readiness 검사에서도 같은 상태를 확인합니다.
- 종료 순서는 readiness 해제, 신규 HTTP 요청 거절, drain 대기, HTTP/gRPC 중단, 진행 중 worker 완료, DB·Redis 종료, metric/trace/profile flush입니다.

## HTTP와 gRPC

REST API와 운영 API는 포트를 분리합니다.

- `HTTP_ADDR`: 비즈니스 HTTP 서버, 기본 `:8080`
- `ADMIN_ADDR`: server의 health, readiness, metric, 선택적 pprof, 기본 `:9090`
- `WORKER_ADMIN_ADDR`: worker의 health, readiness, audit metric, 선택적 pprof, 기본 `:9092`
- `GRPC_ADDR`: 값이 있을 때 gRPC health server 실행

업무 gRPC가 필요해지면 같은 `grpc.Server`에 생성된 service를 등록합니다. REST와 gRPC를 한 포트로 합치는 multiplexing은 실제 운영 요건이 생길 때 추가합니다.

운영 API 계약은 `api/operational.openapi.yaml`, 업무 API 계약은 `api/openapi.yaml`로 분리했습니다. pprof는 `PPROF_ENABLED=true`일 때만 등록하며, admin 포트는 외부에 공개하지 않는다는 배포 정책이 필요합니다.

## Worker offset 정책

Kafka를 사용하는 worker는 `packages/go-platform/kafka.RunConsumer`를 사용합니다. `franz-go` client는 호출자가 직접 만들며 아래 옵션이 필수입니다.

```go
client, err := kgo.NewClient(
    kgo.SeedBrokers(brokers...),
    kgo.ConsumerGroup(group),
    kgo.ConsumeTopics(topics...),
    kgo.DisableAutoCommit(),
    kgo.BlockRebalanceOnPoll(),
)
```

runner는 한 건을 처리한 뒤 handler가 성공한 경우에만 `CommitRecords`를 호출합니다. 실패하거나 종료 context가 취소되면 offset을 commit하지 않아 재전달을 허용합니다. retry topic, DLQ, inbox는 실제 event 계약과 멱등 처리 기준이 정해진 서비스에서 추가합니다.

## 실행

service 저장소 루트에서 모듈로 이동하고 예제 환경 변수를 shell에 주입합니다. 서버와 worker는 DDL을 실행하지 않으므로 배포 전에 migration을 한 번 실행합니다.

```bash
cd services/go-reference-service
cp .env.example .env
set -a
. ./.env
set +a

go run ./cmd/migrate
go run ./cmd/server
go run ./cmd/worker
```

예제 API는 기존 `packages/go-authz/principal.EncodeHeader`로 만든 `X-Principal` header와 재시도용 `Idempotency-Key`를 요구합니다. 이 API는 lock, fencing, transaction, audit outbox를 한 번에 검증하기 위한 예제이므로 실제 서비스에서는 해당 bounded context로 교체합니다.

`X-Principal`은 외부 클라이언트가 임의로 넣는 인증 수단이 아닙니다. Gateway가 외부 header를 제거하고 검증된 사용자 정보로 다시 만든다는 신뢰 경계 안에서만 사용합니다. 예제 route는 `customer` role까지 확인합니다.

이미지는 service repo 루트에서 빌드합니다. worker 배포는 entrypoint를 `/app/worker`, 배포 전 migration Job은 `/app/migrate`로 command를 덮어씁니다.

```bash
docker build -f services/go-reference-service/Dockerfile -t go-reference-service:dev .
```

이 모듈은 복제용 템플릿이므로 `config/services.yml`의 배포 이미지 inventory에는 넣지 않았습니다. 대신 service test workflow가 unit/race/vet, Testcontainers 통합 테스트, 전용 Dockerfile 빌드를 직접 검사합니다. 실제 서비스로 복제한 뒤에는 해당 서비스 이름을 inventory와 배포 설정에 등록합니다.

## 검증

```bash
GOWORK=off go test ./...
GOWORK=off go build ./cmd/server ./cmd/worker ./cmd/migrate

cd ../..
go test -race ./packages/go-audit/... ./packages/go-platform/... ./services/go-reference-service/...
go vet ./packages/go-audit/... ./packages/go-platform/... ./services/go-reference-service/...
```

통합 테스트는 `integration` build tag와 Testcontainers를 사용합니다.

```bash
go test -tags=integration ./services/go-reference-service/tests/integration
```

## 지금 만들지 않은 것

- cache-aside wrapper: `go-redis` 직접 사용으로 충분합니다.
- producer wrapper: `franz-go` producer를 직접 사용하고 event 계약이 생길 때만 공통 정책을 고정합니다.
- 범용 repository interface와 transaction manager: pgx API를 숨기지 않습니다.
- MongoDB outbox: PostgreSQL business transaction과 원자적으로 기록해야 하므로 PostgreSQL outbox를 먼저 사용합니다. MongoDB가 원본 저장소인 서비스에서 별도 구현합니다.
- 범용 inbox: consumer 멱등 key와 side effect 경계가 정해지기 전에는 올바른 schema를 만들 수 없습니다.
