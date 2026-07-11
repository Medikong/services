# Auth Service

`auth-service`는 이메일·휴대폰 Identity, 인증 흐름, Session credential, AccessGrant와 인증 감사 outbox를 소유하는 Go 서비스입니다. 다른 Context와 공유하는 식별자는 `user_id`뿐이며, 이메일·휴대폰·프로필은 JWT claim이나 내부 사용자 헤더에 넣지 않습니다.

## 구조

HTTP controller는 OpenAPI request/response, cookie/header와 ProblemDetails만 처리합니다. 업무 흐름과 transaction 경계는 `internal/application`의 기능별 service가 소유하고, SQL은 각 Aggregate 가까이 있는 PostgreSQL repository에만 둡니다. 범용 `store` 패키지나 메모리 repository는 사용하지 않습니다.

```text
internal/domain/
  intent/          AuthenticationIntent와 암호화된 action payload
  registration/    가입 상태 전이와 user_id 연동 대기
  challenge/       단일 소비 Challenge와 delivery projection
  passwordreset/   decoy를 포함한 PasswordReset 상태
  identity/        Email/Phone Identity와 IdentityLink
  session/         Session, refresh family, credential rotation
  reauth/          목적 한정 1회용 ReauthenticationProof
  access/          UserAuthState와 AccessGrant
  policy/          정책 묶음과 불변 global policy snapshot
  operator/        운영 조회 대상과 수동 작업 aggregate
  outbox/          업무 event의 durable outbox
  inbox/           Context 사용자 event의 durable deduplication inbox
  idempotency/     request fingerprint와 암호화된 credential replay payload

internal/application/
  bootstrap/ signin/ registration/ passwordreset/
  identitymanagement/ session/ actionresume/ operator/ outboxrelay/

internal/transport/http/controller/
  기능별 OpenAPI controller
```

여러 Aggregate를 함께 바꾸는 경우 application service가 하나의 `pgx.Tx`를 열고 Identity, Challenge, Session, audit outbox, domain outbox repository에 같은 transaction을 전달합니다. 외부 Email/SMS, 사용자 계정 연동, 운영 승인 시스템은 주소나 credential을 임의로 넣지 않고 durable outbox/inbox 또는 Port로만 연결합니다.

controller는 `internal/domain` 또는 `pgx`를 직접 import하지 않습니다. application은 transport를, domain은 application·transport와 범용 store/memory repository를 import하지 않습니다. 이 규칙은 각 계층의 `architecture_test.go`에서 검사합니다. refresh rotation은 Session 서비스가 credential 회전, encrypted replay payload, idempotency record, 업무 outbox와 audit outbox를 한 transaction으로 확정합니다. 같은 key의 재시도만 최초 mobile token 묶음을 복구하며, 다른 key로 이전 refresh를 제출하면 family와 Session을 폐기합니다.

이메일 재인증과 휴대폰 교체는 새 Session을 만들지 않습니다. 현재 Session을 이메일 인증 근거로 재바인딩하거나 credential만 회전하고, 기존 web cookie 또는 refresh credential은 일반 인증에서 즉시 제외합니다. 응답 유실 복구는 `rotated_pending_delivery` 상태, 같은 Session·operation·`Idempotency-Key`·request fingerprint와 `AUTH_RECOVERY_TTL`을 모두 만족하는 경우에만 암호화된 최초 결과를 반환합니다. 이 경로는 해당 API의 전용 복구 분기이며 다른 API나 일반 Session 인증에는 사용할 수 없습니다.

운영자 조회·정책·수동 처리 application service는 Session에 남은 role만 신뢰하지 않습니다. 현재 `AccessGrant` version, 명시 permission, 웹 Session과 최근 강한 인증을 다시 확인합니다. 정책 API는 개별 실행 정책 row의 database ID를 노출하지 않고 `auth_policy_global_snapshots`의 단일 immutable version을 ETag로 사용합니다.

가입 완료 전 Auth는 `Auth.RegistrationVerificationCompleted` outbox event만 발행합니다. Context가 되돌려 주는 `User.AuthLinkRequested`는 `registration.Service.ConsumeUserLinkEvent`가 inbox에 먼저 기록하고 같은 transaction에서 두 IdentityLink와 AccessGrant를 반영합니다. 실제 Context 사용자 event topic, Email/SMS provider, 운영 승인 source 주소는 아직 주입하지 않았습니다. `outboxrelay.Publisher`와 `RecordingPublisher` 테스트 Adapter가 그 경계이며, 실주소와 credential이 확정될 때에만 runtime adapter를 바인딩해야 합니다.

## 실행

서비스 저장소 루트에서 실행합니다. server와 worker는 migration을 수행하지 않으므로 배포 또는 로컬 실행 전에 migrate를 한 번 완료해야 합니다.

```bash
cd services/auth-service
cp .env.example .env
set -a
. ./.env
set +a

GOWORK=off go run ./cmd/migrate
GOWORK=off go run ./cmd/server
GOWORK=off go run ./cmd/worker
```

- 업무 HTTP API는 `HTTP_ADDR`(기본 `:8080`)에서 제공합니다.
- server 운영 API는 `ADMIN_ADDR`(기본 `:9090`), worker 운영 API는 `WORKER_ADMIN_ADDR`(기본 `:9092`)에서 제공합니다.
- `/healthz`, `/readyz`, `/metrics`는 운영 포트에만 등록됩니다. `readyz`는 audit·auth schema가 최신이고 PostgreSQL 연결이 가능한지 확인합니다.
- migration이 적용되지 않았거나 최신 버전보다 오래된 경우 server와 worker는 시작을 거부합니다.

## 보안 설정

`.env.example`의 local 기본값은 `local`, `dev`, `development`, `test` 환경에서만 사용할 수 있습니다. 공유 환경에서는 secret manager 또는 KMS가 다음 값을 주입해야 합니다.

- `AUTH_CREDENTIAL_HMAC_KEY`: 최소 32 bytes
- `AUTH_REPLAY_ENCRYPTION_KEY`: 정확히 32 bytes
- `AUTH_JWT_SECRET`: 최소 32 bytes
- `AUTH_JWT_ISSUER`, `AUTH_ALLOWED_ORIGINS`

`staging`, `production` 등 local/test가 아닌 환경은 HTTPS origin, `AUTH_COOKIE_SECURE=true`, 명시적인 secret을 요구합니다. 웹 로그인 credential은 HttpOnly, Secure, SameSite cookie로만 전달하며 모바일은 짧은 access JWT와 회전형 opaque refresh token을 사용합니다.

로그·trace·감사 payload에는 password, token, cookie, 이메일, 휴대폰, 인증번호, owner proof와 provider secret을 남기지 않습니다. 감사 이벤트는 업무 변경과 같은 PostgreSQL transaction에서 `audit_outbox`에 기록되고 worker가 lease/retry/dead-letter 상태로 relay합니다.

## 개발 Virtual Adapter

개발 전용 API와 Virtual Email/SMS Adapter는 `local`, `dev`, `development`, `test` 환경에서만 함께 켤 수 있습니다. 운영 환경에서는 flag, access token, projection key 중 하나라도 설정되면 config 검증에서 시작을 거부합니다.

```bash
AUTH_DEVELOPMENT_ENABLED=true
AUTH_DEV_ROUTE_ENABLED=true
AUTH_VIRTUAL_ADAPTERS_ENABLED=true
AUTH_DEV_ACCESS_TOKEN=replace-with-development-only-gateway-token
AUTH_VIRTUAL_MESSAGE_KEY=replace-with-exactly-32-byte-local-key
```

위 설정으로 실행할 때는 development migration도 적용해야 합니다.

```bash
GOWORK=off go run ./cmd/migrate
GOWORK=off go run ./cmd/server
```

개발 route는 `X-Dev-Access-Token`과 원래 Challenge 소유 증명을 모두 요구합니다. Virtual Adapter는 외부 Email/SMS provider 주소나 credential을 추정하지 않으며, 개발 projection에만 암호화된 verification code를 보관합니다.

## OpenAPI 원장

OpenAPI 원장은 sibling 저장소 `archive/blueprint/50-service-design/A_300_auth/A_300_40-api/openapi/`입니다. 이 서비스에는 CI와 배포 검증용 생성 bundle만 둡니다.

```bash
# archive 원장 변경 후 bundle을 갱신
task auth-openapi-sync

# archive 원장과 snapshot hash, production/dev bundle을 검증
task auth-openapi-check

# archive checkout이 없는 service-only 환경에서는 snapshot 자체만 확인
./services/auth-service/scripts/check-openapi.sh --snapshot-only
```

production bundle에는 `/api/v1/dev/` route가 포함되면 안 됩니다.

## 검증과 이미지

```bash
cd services/auth-service
gofmt -w cmd internal tests
GOWORK=off go test ./...
GOWORK=off go build ./cmd/server ./cmd/worker ./cmd/migrate
GOWORK=off go test -tags=integration -count=1 ./tests/integration

cd ../..
docker build -f services/auth-service/Dockerfile -t auth-service:dev .
```

Docker image의 기본 entrypoint는 `/app/server`입니다. migration Job은 `/app/migrate`, worker 배포는 `/app/worker`로 command를 덮어씁니다.
