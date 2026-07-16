# User Service

사용자 서비스는 사용자 ID, 계정 상태, 프로필, 가입 시점의 필수 동의 이력을 소유합니다. 이메일, 휴대폰, credential, Session, role/permission은 저장하지 않으며 Auth 책임을 가져오지 않습니다.

## 구현 범위

- `users`를 단일 쓰기 원장으로 사용하고 모든 변경을 `user_version`으로 비교합니다.
- `user_agreement_acceptances`, `user_status_history`, `user_idempotency_records`를 같은 PostgreSQL에 저장합니다.
- 가입, 프로필, 프로필 이미지 연결, 계정 상태 변경의 업무 결과와 멱등 결과를 한 트랜잭션에 반영합니다.
- Auth 가입 증거와 Media 자산 증거는 Ed25519 서명, audience, purpose, 만료 시각, 대상 ID를 검증합니다.
- `private_name`은 AES-256-GCM 암호문으로 저장하며 API 응답, 로그, trace에 포함하지 않습니다.
- Event, Inbox, Outbox, Kafka consumer, 복구 worker, 인메모리 저장소를 두지 않습니다.

## 패키지

```text
cmd/server                  API 서버와 종료 신호 처리
cmd/migrate                 세션 잠금이 있는 goose migration
internal/app                DB, HTTP, 관측성 조립과 graceful shutdown
internal/application        User command/query와 트랜잭션 경계
internal/domain/user        User 모델, PostgreSQL 저장소, migration
internal/security           Ed25519 proof와 이름 암호화
internal/transport/http     공개/운영/개발 전용 HTTP route
tests/integration           실제 HTTP와 PostgreSQL Testcontainers 검증
```

## HTTP 경계

업무 API는 `:8080`, `/healthz`, `/readyz`, `/metrics`, 선택적 pprof는 외부에 공개하지 않는 `:9090`에서 제공합니다. 업무 계약은 `api/openapi.yaml`, 개발 전용 proof 발급 계약은 `api/development.openapi.yaml`, 운영 endpoint 계약은 `api/operational.openapi.yaml`에 분리했습니다.

`X-Principal`은 외부 클라이언트가 만드는 인증 수단이 아닙니다. Ingress가 외부의 같은 이름 header를 제거하고 Auth 검증 결과로 다시 만든다는 신뢰 경계에서만 사용합니다. 운영 상태 변경은 strong-auth principal, canonical permission, 허용 Origin, `X-Csrf-Verified: true`를 모두 요구합니다.

개발 proof API는 `SERVICE_ENVIRONMENT`가 local/development/dev/test이고 `USER_DEVELOPMENT_ENABLED=true`일 때만 `/internal/dev/proofs/*`에 등록됩니다. 운영 환경은 개발 flag나 개발 Secret이 하나라도 있으면 시작하지 않습니다.

## 실행

서버는 DDL을 실행하지 않습니다. 같은 이미지의 migration 실행 파일을 배포 전에 별도 Job으로 실행합니다.

```bash
cd services/user-service
cp .env.example .env
set -a
. ./.env
set +a

go run ./cmd/migrate
go run ./cmd/server
```

Migration은 `user_goose_db_version`과 PostgreSQL 세션 잠금을 사용합니다. 서버는 최신 version과 정확히 같은지만 보지 않고 이 바이너리가 지원하는 최소·최대 schema version 범위를 확인합니다.

SIGTERM을 받으면 Readiness를 먼저 내리고 새 업무 요청을 거부합니다. Endpoint 반영 대기 뒤 HTTP listener를 닫아 처리 중 요청을 제한 시간까지 기다린 다음 DB, metric, trace, profiler를 정리합니다.

## 검증

저장소 루트에서 다음 명령을 실행합니다.

```bash
go test -race ./services/user-service/...
go vet ./services/user-service/...
go test -tags=integration -count=1 ./services/user-service/tests/integration
docker build -f services/user-service/Dockerfile -t user-service:dev .
task user-service-check
```

통합 테스트는 PostgreSQL 16 Testcontainer와 실제 HTTP server를 사용합니다. 같은 멱등 요청의 결과 재사용, 같은 키의 다른 요청 거부, 동시 optimistic concurrency 충돌, 상태 이력 단일 기록, proof 검증, drain 상태를 함께 검사합니다.
