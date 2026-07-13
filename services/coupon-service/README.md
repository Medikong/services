# Context 쿠폰 서비스

Context 쿠폰의 캠페인, 발급, 사용, 운영 복구를 맡는 Go 서비스다. 상품·드롭·사용자·주문·결제·CS·정산의 원본은 소유하지 않으며, 검증된 외부 참조와 필요한 불변 스냅샷만 저장한다.

## 실행 단위

- `cmd/server`: 공개·내부 REST API와 `/healthz`, `/readyz`, `/metrics`를 제공한다.
- `cmd/worker`: outbox 전달, inbox 기반 Policy 처리, 비동기 Command 실행, 대량 발급, 발급 재시도, 사용 복구, 쿠폰 만료를 수행한다.
- `cmd/migrate`: PostgreSQL migration을 적용한다.

애플리케이션 코드는 `internal/application`, HTTP Controller는 `internal/transport/http`, Aggregate와 Repository port는 `internal/domain`, PostgreSQL·Redis·관측성 구현은 `internal/platform`에 둔다.

## 상태 보장

PostgreSQL write model과 append-only 원장, partial unique index, 멱등 기록, outbox/inbox가 최종 상태를 보장한다. 업무 상태 변경과 outbox 기록은 같은 트랜잭션에서 끝난다. Redis는 수량 admission 신호를 빠르게 주는 보조 계층이며 PostgreSQL 판정을 대신하지 않는다. 외부 Event 전달과 내부 Policy 처리는 같은 Event ID를 사용해 재실행해도 중복 결과가 생기지 않는다.

Worker는 `SKIP LOCKED`와 lease로 작업을 점유하고 지수 backoff로 재시도한다. 재시도 한도는 `HOTSPOT.A.19-06`이 확정되지 않았으므로 배포 환경에서 명시한다. 발급 재시도가 한도에 도달하면 자동 재처리를 멈추되 `failed_final`로 확정하지 않고, 승인된 `CMD.A.19-22` 입력을 기다린다. 사용 복구의 종단 실패는 `CMD.A.19-33`으로 복구 결과를 기록한 뒤 승인된 최종 처리를 기다린다.

## 계약

서비스에 보관한 OpenAPI snapshot은 `api/openapi/openapi.bundle.yaml`이다. 원천과 snapshot 동기화는 다음 명령으로 확인한다.

```bash
./scripts/sync-openapi.sh
./scripts/check-openapi.sh
```

25개 HTTP operation, 34개 Command, 41개 Event, 22개 Policy, 8개 Aggregate의 문서 ID와 코드 위치는 `internal/domain/catalog/catalog.go`에서 관리한다. `internal/app/traceability_test.go`가 HTTP 처리와 durable dispatcher의 Command 합집합, Policy registry, Event projection coverage를 원천 개수와 대조한다. 판매자 성과 조회 모델 `RM.A.19-03`은 `HOTSPOT.A.19-07`이 결정될 때까지 만들지 않는다.

## 로컬 검증

PostgreSQL을 준비하고 `.env.example`의 설정을 적용한 뒤 실행한다.

```bash
go run ./cmd/migrate
go run ./cmd/server
go run ./cmd/worker
go test ./...
go test -tags=integration ./...
```

운영 환경에서는 코드 해시 키, 허용 Origin, Worker lease·재시도 정책을 반드시 명시한다. Gateway의 service Principal은 `serviceId`를 포함해야 하며, `WorkloadAuthorizationPort`가 operation과 경로 resource를 기준으로 역할·소유 범위를 판정한다. 결제 결과와 CS case port는 각각 order·redemption과 target user·운영 작업의 결합 관계까지 검증한다.

아직 HTTP 주소가 확정되지 않은 외부 Context port는 로컬·개발·테스트 환경의 기본 조립에서만 fail-closed adapter를 사용한다. 그 밖의 환경에서 `NewServer`와 `NewWorker`는 외부 의존성을 주입하지 않으면 시작 단계에서 실패한다. 실제 배포 조립은 `NewServerWithExternalDependencies`와 `NewWorkerWithExternalDependencies`에 인증된 REST 또는 Event adapter를 모두 주입해야 한다. `OperationsCommands` source는 승인된 `CMD.A.19-22`와 실패 원본 `CMD.A.19-34`를 durable ingress에 연결한다. 브로커와 topic은 확정하지 않았으므로 이 hook에 배포 환경의 adapter를 주입하며, 외부 확인 실패를 성공으로 바꾸면 안 된다.

## 미확정 정책

`HOTSPOT.A.19-01~09`는 런타임 설정이나 외부 계약으로 남아 있다. 사용자에게 보여 줄 접수·완료 표현, 사용 확정 사건, 예약 유예·재사용, 정책 변경의 기존 쿠폰 영향, 승인선과 보상 한도, 재시도 횟수·간격, 판매자 성과 권한, 중복 적용 조합, 자동 지급 원천 계약을 이 서비스가 임의로 확정하지 않는다.
