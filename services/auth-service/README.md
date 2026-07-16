# Auth Service

`auth-service`는 인증 Identity, PasswordCredential, VerificationChallenge, Registration, PasswordReset, AuthenticationIntent, Session, 회전형 refresh credential과 UserAuthState를 소유합니다. `user_id`는 User 서비스가 만든 외부 식별자를 참조하며 프로필, 동의, role, permission, membership은 저장하거나 JWT에 포함하지 않습니다.

## 실행 단위

- `cmd/server`: 공개 HTTP `:8080`, 운영 HTTP `:9090`
- `cmd/worker`: 도메인 Outbox 및 감사 relay, 운영 HTTP `:9092`
- `cmd/migrate`: PostgreSQL core/audit schema와 개발 전용 schema 적용

PostgreSQL이 원장입니다. 도메인별 PostgreSQL repository는 각 `internal/domain/<feature>`에 있고 `internal/app`은 조립과 lifecycle만 담당합니다.

## 보안 계약

- access JWT는 RS256이며 `iss`, `sub`, `sid`, `aud`, `iat`, `exp`, `jti`만 발급합니다.
- `GET /.well-known/jwks.json`은 active/retiring 공개키, `Cache-Control`, `ETag`를 제공합니다.
- refresh token은 opaque 값이며 DB에는 keyed hash만 저장합니다. 회전된 token 재사용은 같은 family와 Session을 폐기합니다.
- User 계정 상태 proof는 User 서비스의 Ed25519 서명, `aud=auth-service`, `purpose=apply_user_status`, 만료와 path 사용자 일치를 검증합니다.
- 운영 API의 `X-Authorization-Decision`은 별도 인가 경계 port로 검증합니다. 검증 adapter가 주입되지 않으면 기본값은 거부입니다.
- 운영 환경에서 개발 Route나 가상 Email/SMS adapter 설정이 하나라도 활성화되면 시작을 거부합니다.

가입 완료는 비동기 이벤트를 기다리지 않습니다. API.A.300-05가 Auth 서명의 `registrationCompletionProof`를 발급하고, User 서비스가 사용자를 만든 뒤 API.A.300-06에 User 서명의 `userCreationProof`를 제출하면 Auth가 IdentityLink, UserAuthState, Session을 한 트랜잭션에서 확정합니다.

도메인 Outbox relay는 `NewWorkerWithPublisher`에 배포 환경의 신뢰할 수 있는 publisher adapter를 주입했을 때만 실행됩니다. 저장소에는 아직 특정 broker의 주소·topic·credential 기준이 없으므로 기본 실행 파일은 이를 추측하지 않고 감사 relay만 시작합니다.

RSA 개인키 예시는 다음처럼 만들 수 있습니다. 실제 환경에서는 파일을 저장소에 넣지 않고 secret manager가 `AUTH_JWT_PRIVATE_KEY_PEM`으로 주입해야 합니다.

```bash
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:3072
```

## 실행

```bash
cp .env.example .env
# .env의 필수 key와 DATABASE_URL을 채운 뒤
set -a
. ./.env
set +a
GOWORK=off go run ./cmd/migrate
GOWORK=off go run ./cmd/server
```

## OpenAPI 원장

OpenAPI 원장은 sibling `archive/blueprint/50-service-design/A_300_auth/A_300_40-api/openapi/` 하나입니다. 서비스에는 배포와 CI용 bundle 및 원장 hash만 둡니다.

```bash
task auth-openapi-sync
task auth-openapi-check
```

운영 bundle에는 `/api/v1/dev/` Route가 들어가면 검사가 실패합니다. `archive` checkout이 없는 CI에서는 `scripts/check-openapi.sh --snapshot-only`로 bundle 자체를 검증합니다.

## 검증

```bash
GOWORK=off go test ./...
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
GOWORK=off go test -tags=integration -count=1 ./tests/integration
docker build -f services/auth-service/Dockerfile -t auth-service:dev .
```

이미지 기본 entrypoint는 `/app/server`입니다. migration Job은 `/app/migrate`, worker 배포는 `/app/worker`를 실행합니다.
