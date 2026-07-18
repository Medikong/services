# Auth Service

`auth-service`는 인증 Identity, PasswordCredential, VerificationChallenge, Registration, PasswordReset, AuthenticationIntent, Session, 회전형 refresh credential과 UserAuthState를 소유합니다. `user_id`는 User 서비스가 만든 외부 식별자를 참조하며 프로필, 동의, role, permission, membership은 저장하거나 JWT에 포함하지 않습니다.

## 실행 단위

- `cmd/server`: 공개 HTTP `:8080`, 운영 HTTP `:9090`
- `cmd/worker`: 도메인 Outbox 및 감사 relay, 운영 HTTP `:9092`
- `cmd/migrate`: PostgreSQL core/audit schema와 개발 전용 schema 적용

PostgreSQL이 원장입니다. 도메인별 PostgreSQL repository는 각 `internal/domain/<feature>`에 있고 `internal/app`은 조립과 lifecycle만 담당합니다.

## 보안 계약

- access JWT protected header는 `alg=RS256`, JWKS와 일치하는 `kid`, `typ=JWT`이며 claim은 `iss`, `sub`, `sid`, `aud`, `iat`, `exp`, `jti`만 발급합니다.
- `GET /.well-known/jwks.json`은 active/retiring 공개키, `Cache-Control`, `ETag`를 제공합니다.
- refresh token은 opaque 값이며 DB에는 keyed hash만 저장합니다. 회전된 token 재사용은 같은 family와 Session을 폐기합니다.
- User 계정 상태 proof는 User 서비스의 Ed25519 서명, `aud=auth-service`, `purpose=apply_user_status`, 만료와 path 사용자 일치를 검증합니다.
- 운영 API의 `X-Authorization-Decision`은 별도 인가 경계 port로 검증합니다. 검증 adapter가 주입되지 않으면 기본값은 거부입니다.
- 운영 환경에서 개발 Route나 가상 Email/SMS adapter 설정이 하나라도 활성화되면 시작을 거부합니다.

Auth는 access JWT나 내부 인증 헤더에 role, permission, email, membership 또는 업무 ACL을 넣지 않습니다. 리소스 소유권은 인증된 `X-User-Id`와 도메인 데이터의 owner를 비교해 각 업무 서비스가 판정하며, 불일치는 해당 서비스가 `403`으로 반환합니다.

### 현재 코드와 배포 blocker

현재 저장소 코드의 access JWT signing과 JWKS는 RS256입니다. 다만 issuer와 active deployment에는 다음 차이가 남아 있습니다.

- 현재 config는 `AUTH_JWT_ISSUER`가 없으면 `ServiceName`(`auth-service`)으로 fallback하고 operational validation도 fallback 사용을 거부하지 않습니다. 운영에서 issuer를 명시적으로 요구하고 기본값을 두지 않는 것은 승인된 목표 정책이며 아직 구현 사실이 아닙니다.
- 공통 `values/services/auth.yaml`은 legacy `AUTH_JWT_SECRET`을 선언하지만 Helm의 목록 coalescing에서 private-dev/aws-dev overlay의 전체 `container.env` 목록으로 대체됩니다. 따라서 두 active effective stack에는 각각 legacy `JWT_SECRET` 하나만 남습니다. 저장소의 dev overlay도 `JWT_SECRET`을 선언하지만 두 active Application에서 참조하지 않습니다.
- 활성 Kong shared resources는 HS256 JWT credential을 render합니다.
- 활성 private-dev/aws-dev values stack은 현재 코드가 요구하는 RS256 private key와 key ID를 공급하지 않습니다.
- 활성 Kong `ticketing-identity-headers`는 JWT의 email/role claim을 읽어 `X-User-Email`/`X-User-Role`을 만들고, `ticketing-role-*`는 role claim으로 인가와 legacy `403`을 결정합니다. Notification, Interest, Order, Payment ingress attachment가 남아 있습니다.
- Notification과 Interest runtime은 아직 `X-User-Role`을 신뢰해 role 기반 접근을 판정합니다.

따라서 현재 GitOps desired state는 최신 Auth 코드와 호환되는 RS256-ready 또는 identity-only-ready 배포가 아닙니다. 후속 Auth config 작업은 운영 issuer fallback을 거부해야 하고, 후속 GitOps 작업은 legacy JWT secret/Kong credential과 role/email plugin 경로를 제거한 뒤 private key, key ID, issuer를 승인된 Secret-backed 경로로 공급해야 합니다. Notification/Interest 후속 작업도 role header trust를 제거해야 합니다. 이 README 변경은 배포 manifest나 서비스 runtime을 수정하거나 blocker를 해소하지 않습니다.

### Istio HTTP ext_authz 계약 및 배포 상태

> 구현 상태: 현재 stage worktree의 runtime 소스는 `/internal/ext-authz`와 모든 하위 path의 Handler를 router에 등록합니다. 이 경로는 공개 OpenAPI에 노출하지 않습니다. 활성 GitOps는 아직 legacy JWT/Kong 구성이고 해당 Handler를 배포하거나 외부 route/Istio ext_authz에 연결하지 않았습니다. RS256 key/issuer 공급과 Istio 연결은 후속 GitOps 작업입니다.

Istio는 외부 요청의 `X-User-*`, `X-Session-*`, `X-Token-*`을 먼저 제거하고 JWT를 검증한 뒤 Auth의 `/internal/ext-authz`에 HTTP check를 보냅니다. request body는 전달하지 않고 `Authorization`, `X-Request-Id`, 원래 method/path만 전달합니다.

| 결과 | HTTP | Auth adapter 출력 | 책임 |
| --- | --- | --- | --- |
| identity 확인 | `200` | `X-User-Id`, `X-Session-Id`, `X-Token-Id` | Auth Session 상태 검사 |
| JWT/Session 인증 거부 | `401` | 없음 | Istio/Auth |
| Auth/JWKS/Redis/PostgreSQL 상태 미확정 | `503` | 없음 | Istio/Auth, fail closed |
| 인증 뒤 resource ownership 불일치 | `403` | 해당 없음 | 업무 서비스 |

세 성공 헤더 외에 role, permission, email 등은 만들지 않습니다. `/internal/ext-authz`는 내부 전용 runtime 경로이며 외부 Gateway Route와 공개 OpenAPI에 등록하지 않습니다. 지정된 Ingress workload identity만 호출하도록 제한하는 것은 후속 GitOps/Istio 정책의 책임입니다. Handler의 초기 timeout은 200ms이며 자동 재시도와 fail-open이 없습니다.

Route는 명시적 allowlist로 분류합니다. 공개 가입/로그인/비밀번호 재설정 Route는 Auth flow credential을, 보호 Route와 `GET /api/v1/auth/context`는 JWT와 Session을, refresh/logout Route는 refresh credential을 검증합니다. JWKS Route는 사용자 credential 대신 mesh network policy로 제한합니다.

### 서비스 토폴로지

- User 서비스는 canonical 사용자 ID와 계정 상태의 후보 서비스이며, 별도 검증 gate를 통과하기 전에는 운영 canonical로 승격하지 않습니다.
- Backoffice는 Auth/User와 분리된 retention 경계이고 현재 비활성입니다. 이 계약은 Backoffice Route나 workload를 활성화하거나 Backoffice를 User로 대체하지 않습니다.

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
