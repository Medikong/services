# Go Programming Rule

이 문서는 service repo의 짧은 Go 프로그래밍 규칙입니다. 아키텍처 문서보다 짧게 유지합니다. 설명이 길어지는 내용은 이 문서에 모두 풀어쓰기보다 관련 문서로 연결합니다.

## 기술 스택

| 영역 | 기본 후보 |
| --- | --- |
| Backend | Go, standard library, `context`, `errors`, `net/http`, `log/slog` |
| HTTP & API | `net/http`, oapi-codegen |
| Data & Messaging | PostgreSQL, MongoDB, Kafka, Redis, pgx, database/sql, sqlc, mongo-go-driver, go-redis |
| Migration | goose, golang-migrate |
| Messaging Client | segmentio/kafka-go, confluent-kafka-go, Watermill |
| Async Jobs | Asynq, Watermill, Kafka consumer worker |
| Auth & Security | golang-jwt/jwt, bcrypt, argon2, JOSE/JWK 라이브러리 |
| Config | envconfig, cleanenv, viper |
| Platform | Docker, Kubernetes, Istio |
| CI/CD & IaC | GitHub Actions, Helm, Argo CD, Terraform, AWS, Amazon ECR |
| Logging | `log/slog`, `packages/go-platform/logger` |
| Observability | OpenTelemetry Go, Prometheus client_golang, Prometheus, Alertmanager, Grafana, Loki, Tempo |
| Error Handling | github.com/samber/oops, errors.Is, errors.As |
| Resilience & Concurrency | cenkalti/backoff, sony/gobreaker, x/sync/errgroup, singleflight |
| Validation | go-playground/validator, ozzo-validation |
| Quality & Test | testing, httptest, testify, testcontainers-go, gomock, mockery, k6, Postman, Newman, Trivy |
| Static Analysis | gofmt, go vet, staticcheck, golangci-lint, govulncheck |
| CLI & Task | cobra, Taskfile |
| Docs & API | OpenAPI, Swagger, Swagger UI, oapi-codegen |
| Reference Repositories | zeromicro/go-zero |

## 핵심 원칙

- 책임 경계를 지킵니다. composition root는 의존성을 조립하고, 도메인 service는 role interface와 repository interface만 사용하며, storage, message broker, provider 구현 세부사항은 service로 새지 않게 합니다.
- 공통 기능은 `packages/go-*` 아래에 둡니다. 서비스 루트 바로 아래에 임의 공통 패키지를 만들지 않습니다.
- 변경 범위는 좁게 유지합니다. 현재 경계 안에서 해결할 수 있는 요청에 불필요한 대형 리팩터링을 붙이지 않습니다.
- 실패를 빈 성공처럼 숨기지 않습니다. unsupported capability, route 없음, remote error, storage error, invalid input은 명시적인 error로 반환하고, 가능한 경우 service, domain, operation, resource id, provider, status 맥락을 포함합니다.

## Ponytail 구현 원칙

개발 작업에서 불필요한 래핑, 추상화, boilerplate 판단이 필요하면 `$ponytail` 스킬을 기본으로 사용합니다. 기본 강도는 `full`입니다.

- 먼저 이 코드가 정말 필요한지 확인합니다. 추측성 확장, 나중을 위한 interface, 구현체 하나뿐인 factory, 변하지 않는 값을 위한 config는 만들지 않습니다.
- 이미 repo 안에 있는 helper, 타입, 패턴을 먼저 재사용합니다. 그다음 표준 라이브러리, 플랫폼 기본 기능, 이미 설치된 의존성 순서로 검토합니다.
- 단순히 외부 라이브러리, 표준 라이브러리, SDK API 이름만 바꾸는 wrapper는 만들지 않습니다.
- 래핑은 구조적 이유가 있을 때만 허용합니다. 예: 실제로 여러 구현체가 필요하거나, 테스트 대체 지점이 현재 필요하거나, 프로젝트 실행 정책(timeout, retry, pool, readiness, migration, telemetry correlation)을 한곳에 모아야 하거나, 도메인 경계를 보호해야 하는 경우입니다.
- OOP식 구조가 필요한 경우에도 가장 작은 구조부터 시작합니다. `interface`, `factory`, `manager`, `provider`, `adapter` 이름을 붙이기 전에 concrete 함수나 타입으로 충분한지 먼저 확인합니다.
- 추가보다 삭제를 우선합니다. 파일 수와 diff는 가능한 작게 유지하되, 실제 호출 경로를 이해한 뒤 가장 작은 변경을 선택합니다.
- 한계가 있는 의도적 단순화는 `// ponytail:` 주석으로 한계와 업그레이드 기준을 남깁니다.
- trust boundary의 validation, 데이터 손실을 막는 error handling, 보안, 접근성, 사용자가 명시적으로 요구한 동작은 단순화를 이유로 제거하지 않습니다.

## 도메인 기반 패키지 구조

- 기본 구조는 계층형 `model/`, `service/`, `repository/`, `store/` 폴더가 아니라 도메인 단위 패키지입니다.
- 폴더와 패키지는 기술 계층이 아니라 의존 관계와 도메인 경계를 나타냅니다. `controller.go`, `service.go`, `repository.go` 같은 파일 역할을 같은 이름의 공용 패키지로 모으지 않습니다.
- `internal` 바로 아래 1차 레벨은 `app`, `domain`, `platform`, `common`을 기본으로 합니다. `transport`는 기본 패키지가 아닙니다.
- `internal/app`은 composition root입니다. dependency wiring만 담당하고 도메인 로직을 넣지 않습니다.
- `internal/domain`은 서비스 내부 도메인 규칙, use case, controller, route, repository port와 도메인이 소유한 persistence/message adapter를 함께 둡니다.
- 각 도메인은 자신의 HTTP route와 controller를 직접 소유합니다. gRPC service나 message consumer가 필요할 때도 같은 도메인 패키지에 둡니다.
- 중앙 router 패키지에서 모든 도메인 controller를 나열하거나 API 경로를 대신 등록하지 않습니다. 각 도메인은 `routes.go` 같은 파일로 자신의 method와 path를 controller에 연결합니다.
- `internal/app`은 공통 HTTP router나 worker runtime을 만들고 도메인별 등록 함수를 호출할 수 있지만, method, path, request/response 변환과 업무 규칙을 직접 구현하지 않습니다.
- HTTP 서버 설정과 공통 middleware는 표준 라이브러리와 `packages/go-platform` 구현을 먼저 사용합니다. broker client, connection policy처럼 서비스별 통합 정책이 필요한 실행 기술만 `internal/platform`에 둡니다.
- `internal/transport/http`, `internal/transport/grpc`, `internal/transport/worker`처럼 전달 기술별 폴더를 기본 골격으로 만들지 않습니다. 기술 종류가 아니라 기능을 소유한 도메인 패키지에서 진입점을 찾을 수 있어야 합니다.
- `internal/platform`은 외부 라이브러리를 다시 감싸는 얇은 wrapper가 아니라, 이 프로젝트의 실행 정책과 기술 통합 지점을 담습니다. 설정 로딩, connection policy, readiness, migration, telemetry bootstrap처럼 여러 도메인이 공유하는 프로젝트 기준 코드만 둡니다.
- `internal/common`은 서비스 내부 여러 도메인이 공유하는 순수 유틸리티만 둡니다. 비즈니스 규칙, 외부 기술 통합 정책, 특정 도메인 개념을 넣지 않습니다.
- 특정 도메인의 SQL, MongoDB, Redis, Kafka 구현은 전역 `store/` 또는 `adapter/` 폴더가 아니라 해당 도메인 패키지 안에 둡니다.
- 단순히 외부 라이브러리 API 이름만 바꾸는 wrapper는 만들지 않습니다. 프로젝트 공통 기본값, timeout, pool, retry, readiness, migration, telemetry correlation 같은 정책이 있을 때만 `internal/platform`에 둡니다.
- 서비스 간 공유 계약과 공통 데이터 모델은 `internal`에 두지 않습니다. 언어 중립 계약은 `contracts/`, generated Go 계약 타입은 `packages/go-contracts`에 둡니다.
- `common`, `platform`이 비대해지면 먼저 도메인에 둘 수 없는지 확인합니다.

기본 골격은 다음과 같습니다.

```text
services/<service-name>/
├── cmd/
│   └── server/
│       └── main.go
├── api/
│   └── openapi.yaml
├── internal/
│   ├── app/
│   │   ├── server.go
│   │   └── worker.go
│   ├── domain/
│   │   ├── <domain-a>/
│   │   │   ├── routes.go
│   │   │   ├── controller.go
│   │   │   ├── model.go
│   │   │   ├── command.go
│   │   │   ├── service.go
│   │   │   ├── repository.go
│   │   │   ├── postgres_repository.go
│   │   │   ├── redis_repository.go
│   │   │   ├── publisher.go
│   │   │   ├── consumer.go
│   │   │   ├── validation.go
│   │   │   └── errors.go
│   │   └── <domain-b>/
│   │       ├── routes.go
│   │       ├── controller.go
│   │       ├── model.go
│   │       ├── service.go
│   │       ├── repository.go
│   │       └── postgres_repository.go
│   ├── platform/
│   │   ├── config/
│   │   ├── database/
│   │   ├── cache/
│   │   ├── messaging/
│   │   ├── migration/
│   │   └── observability/
│   └── common/
│       ├── idgen/
│       ├── pagination/
│       ├── normalizer/
│       ├── validator/
│       └── testutil/
├── tests/
│   ├── integration/
│   └── e2e/
└── README.md
```

위 파일은 배치 위치를 보여 주는 예시입니다. 실제 도메인에 필요한 파일만 만들고, 사용하지 않는 controller, consumer, repository 골격을 미리 생성하지 않습니다.

예를 들어 쿠폰 도메인의 Redis admission gate는 `internal/domain/coupon/redis_gate.go`에 둡니다. 쿠폰 발급 SQL은 `internal/domain/coupon/postgres_repository.go`에 둡니다. Redis/Valkey를 어떤 timeout, pool, readiness 정책으로 붙일지는 `internal/platform/cache`에 둡니다. PostgreSQL 연결 정책과 readiness는 `internal/platform/database`, migration runner는 `internal/platform/migration`에 둡니다. 트랜잭션 경계는 공용 platform 패키지로 숨기지 않고, 해당 도메인 use case나 repository 구현에서 명시적으로 드러냅니다.

쿠폰 HTTP API가 있다면 method와 path 등록은 `internal/domain/coupon/routes.go`, 요청·응답 변환은 `internal/domain/coupon/controller.go`가 맡습니다. `internal/app/server.go`는 표준 라이브러리나 `packages/go-platform/httpserver`로 서버와 공통 middleware를 준비하고 쿠폰 route를 등록할 뿐, 쿠폰 API 경로를 다시 선언하지 않습니다.

## 함수 설계

- 함수 이름은 미니멀하게 둡니다. 패키지명, 타입명, 수신자에서 이미 드러나는 문맥을 함수 이름에 반복하지 않습니다.
- I/O, remote call, storage, 오래 걸리는 작업처럼 취소와 timeout이 필요한 함수는 `context.Context`를 첫 인자로 받습니다. 순수 계산이나 단순 값 변환 함수에는 억지로 context를 넣지 않습니다.
- 함수 인자는 필수 값과 자주 바뀌는 값을 먼저 드러냅니다. 선택적 설정, 확장 가능한 설정, 호출자별 조정값은 일반 인자로 계속 늘리지 않습니다.
- 인자가 복잡해지거나 선택값이 늘어나면 `With...` 형태의 함수형 옵션 패턴을 우선 사용합니다. option은 명시적으로 검증하고, 잘못된 option을 조용히 무시하지 않습니다.

## 라이브러리 도입

- 표준 라이브러리와 repo-local 공통 패키지를 먼저 검토합니다.
- 새 라이브러리를 추가할 때는 사용 목적, 대체안, 운영 영향, 테스트 전략을 README나 PR 설명에 남깁니다.
- 서비스 공통 기능은 `packages/go-*` 아래에 둡니다. 서비스 루트 바로 아래에 임의 공통 패키지를 만들지 않습니다.

## 참고 레포지토리

- `zeromicro/go-zero`는 Go microservice framework, `goctl` code generation, resilience, service context, API/RPC 구조를 참고합니다.
- 이 repo의 구조를 그대로 복사하지 않습니다. `handler/logic/svc/types` 생성 구조는 go-zero framework에 맞춘 구조이므로, service repo의 기본 구조는 도메인 기반 패키지 규칙을 우선합니다.
- 참고 범위는 code generation, timeout, rate limit, circuit breaker, load shedding, validation, service context wiring, observability integration입니다.

## 에러 처리

- 직접 작성하는 Go 코드는 error 생성, wrapping, joining에 `github.com/samber/oops`를 사용합니다.
- 에러는 명시적으로 전파합니다. 실패를 성공 값, 빈 값, nil error, silent fallback으로 바꾸지 않습니다.
- 새 error를 만들 때 `fmt.Errorf`, `errors.New`, `errors.Join`을 사용하지 않습니다. error 판별을 위한 `errors.Is`, `errors.As`는 사용할 수 있습니다.
- 생성 코드는 이 규칙에서 제외합니다. 생성 코드는 다시 생성될 수 있으므로 직접 수정하지 않습니다.
- 새 error는 `oops.New` 또는 `oops.Errorf`를 사용합니다. 하위 레이어 error를 원인으로 보존해야 할 때는 `Wrap` 또는 `Wrapf`를 사용합니다. cleanup 과정에서 여러 error를 보존해야 할 때는 `oops.Join`을 사용합니다.
- 같은 함수 안에서 domain과 context가 반복되면 재사용 가능한 builder를 먼저 만듭니다. builder는 `.New`, `.Errorf`, `.Wrap`, `.Wrapf`, `.Join`, `.Recover`, `.Recoverf` 같은 종료 메서드로 끝냅니다.

```go
errb := oops.
	In("coupon_repository").
	With(
		"service", "coupon-service",
		"operation", "issue_coupon",
		"coupon_id", command.CouponID,
		"user_id", command.UserID,
	)

tx, err := r.db.BeginTx(ctx, nil)
if err != nil {
	return errb.Wrap(err)
}
```

- context는 HTTP handler나 CLI 경계에서 한 번에 몰아서 붙이지 않습니다. 호출자는 자신이 알고 있는 요청 맥락을 붙이고, 호출받는 쪽은 자신이 알고 있는 domain 맥락을 붙입니다.
- `With(...)`는 구조화된 context이며, 항상 사람이 읽는 메시지를 대체하지는 않습니다. 테스트나 운영자가 `service=auth-service`, `operation=login`, `status=502`, `user_id=...` 같은 필드를 error 문자열에서 직접 확인해야 한다면 해당 필드를 메시지에도 남깁니다.

## Validation

- request/DTO 구조 검증에는 `github.com/go-playground/validator`를 우선 검토합니다.
- 도메인 규칙, 조건부 검증, 여러 필드가 함께 만드는 invariant에는 `github.com/go-ozzo/ozzo-validation`을 함께 검토합니다.
- validation 실패는 명시적인 error로 반환합니다. HTTP handler는 validation error를 일관된 error response로 매핑합니다.

## Logging & Observability

- 로깅은 애플리케이션 이벤트를 구조화된 로그로 남기는 책임입니다. Go 서비스는 `log/slog` 기반 wrapper를 사용합니다.
- 새 JSON logger나 독자 logger framework를 만들지 않습니다.
- 관측성은 metric, trace, dashboard, alert를 다루는 책임입니다.
- 로그 라이브러리를 관측성 SDK처럼 쓰거나, 관측성 SDK를 logger 대체재로 쓰지 않습니다.

## Storage

- PostgreSQL repository는 해당 도메인의 repository interface를 구현합니다. 도메인 service가 SQL이나 driver-specific type에 의존하지 않게 합니다. 트랜잭션이 필요한 경우 해당 use case의 일관성 경계를 코드에서 명시적으로 드러냅니다.
- MongoDB가 필요한 서비스는 collection validator와 index를 runtime 또는 storage package spec으로 관리합니다. repository 메서드마다 같은 invariant를 반복 방어하지 않습니다.
- repository 구현체는 가능한 한 package 내부 타입으로 두고, 생성자는 도메인의 repository interface를 반환하거나 interface에 맞는 concrete를 반환합니다.
- repository 생성 시점에 결정되는 invariant는 생성자에서 검증합니다.

## Messaging & Async

- Redis 기반 background job은 Asynq를 우선 검토합니다.
- 이벤트 라우팅, pub/sub, broker 추상화가 필요한 경우 Watermill 또는 Kafka consumer worker를 검토합니다.
- 메시지 handler는 ack, retry, idempotency, dead-letter 정책을 명시합니다. 실패를 로그만 남기고 성공 ack로 처리하지 않습니다.

## Testing

- 단위 테스트에서는 `github.com/stretchr/testify`를 적극적으로 사용합니다. 실패 시 이후 검증이 의미 없으면 `require`를 우선하고, 같은 상태에서 여러 값을 함께 확인할 때는 `assert`를 사용할 수 있습니다.
- 테스트 helper는 실패를 숨기지 않습니다. helper 안에서 테스트를 중단해야 한다면 `t.Helper()`와 `require`로 실패 위치를 호출자 기준으로 드러냅니다.
- Mock은 interface fake를 먼저 고려합니다. framework가 필요하면 `gomock`, `mockery`, `testify/mock` 중 하나를 목적에 맞게 고릅니다.
- 기본 `go test ./...`는 빠르고 재현 가능한 단위 테스트와 가벼운 통합 테스트를 대상으로 합니다. 실제 외부 API 호출이나 사용자의 로컬 환경에 강하게 묶인 테스트는 기본 경로에 넣지 않습니다.
- 단위 테스트는 함수, 메서드, 작은 패키지 단위를 검증합니다. 외부 의존성은 interface fake, stub, fake HTTP transport, `httptest`로 대체합니다.
- DB, Kafka, Redis 같은 외부 의존 통합 테스트는 가능한 한 `testcontainers-go`를 사용합니다.
- 빌드된 CLI 실행, 외부 프로세스, 실제 DB 서버, 실제 provider API처럼 느리거나 환경 의존성이 큰 검증은 `integration` 또는 `e2e` build tag로 분리합니다.
- e2e 테스트는 사용자가 만나는 경계에서 검증합니다. HTTP API는 실제 server boundary에서 status code, response body, headers, side effect를 확인합니다.

## API Documentation

- API 계약은 OpenAPI를 우선합니다.
- Swagger 또는 Swagger UI는 OpenAPI를 사람이 확인하기 위한 문서화 표면으로 사용합니다.
- handler 구현과 OpenAPI 문서가 갈라지지 않게 endpoint, request, response, error code 변경을 함께 반영합니다.

## Documentation

- 아키텍처 계약은 `docs/architecture` 아래에 둡니다.
- 프로그래밍 규칙은 `docs/programming` 아래에 둡니다.
- 서비스별 구현 범위와 실행 방법은 각 `services/<service-name>/README.md`에 둡니다.
