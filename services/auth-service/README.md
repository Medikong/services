# auth-service

인증 계정, 인증 수단, 세션, role 부여, HS256 JWT access token 발급/검증을 담당하는 Go 서비스입니다.

auth-service는 `user_id`와 인증/세션/권한만 다룹니다. 사용자 프로필, 실명, 닉네임처럼 "사용자가 누구인지"를 설명하는 정보는 user-service 책임입니다.

## 구조

- `internal/app`: DB, migration, repository, service, HTTP route wiring
- `internal/domain/account`: auth account 생성, 이메일 가입/로그인 오케스트레이션
- `internal/domain/credential`: password credential 저장과 이메일 credential 조회
- `internal/domain/userlink`: `auth_account_id -> user_id` 연결
- `internal/domain/session`: JWT access token, opaque refresh token, refresh rotation, revoke, introspection
- `internal/domain/rolegrant`: account role grant 저장과 조회
- `internal/domain/principal`: Principal 생성과 authz cache
- `internal/domain/dev`: local E2E용 test JWT 발급
- `internal/platform/config`: 환경 설정 로딩
- `internal/platform/database`: PostgreSQL 연결, migration, transaction boundary helper, token/id helper
- `internal/transport/http`: public auth, session, introspection, dev, operational route

## 실행

```bash
DATABASE_URL=postgres://app:app@localhost:5432/auth_service?sslmode=disable \
JWT_SECRET=local-auth-secret \
go run ./cmd/server
```

기본 주소는 `:8080`입니다. `HTTP_ADDR`로 변경할 수 있습니다. `DATABASE_URL`이 없으면 인메모리 fallback 없이 초기화 오류로 실패합니다.
`JWT_SECRET`이 없으면 JWT 서명을 할 수 없으므로 초기화 오류로 실패합니다.

## 토큰 계약

- access token은 `contracts/jwt-conventions.md`를 따르는 HS256 JWT입니다.
- JWT claim은 `iss`, `sub`, `role`, `iat`, `exp`, `jti`를 포함합니다. 이메일 같은 개인정보는 access token claim에 넣지 않습니다.
- `iss` 기본값은 `auth-service`이고 `JWT_ISSUER`로 바꿀 수 있습니다.
- `role`은 `CUSTOMER`, `PROVIDER`, `ADMIN` 중 하나입니다. legacy 입력값 `customer`, `seller`, `operator`는 내부에서 계약 enum으로 정규화합니다.
- refresh token은 `rtk_...` opaque string입니다. 서버에는 refresh token 원문이 아니라 SHA-256 hash만 저장합니다.
- refresh 성공 시 기존 access token의 `jti`와 기존 refresh token은 더 이상 유효하지 않습니다.
- logout/revoke는 세션을 `revoked`로 바꾸며 같은 `jti`의 JWT introspect를 실패시킵니다.
- `auth_accounts.status = disabled`인 계정은 login과 introspect가 모두 실패합니다.

## 환경 변수

| 이름 | 기본값 | 설명 |
| --- | --- | --- |
| `DATABASE_URL` | 없음 | PostgreSQL DSN. 없으면 시작 실패 |
| `JWT_SECRET` | 없음 | HS256 signing key. 없으면 시작 실패 |
| `JWT_ISSUER` | `auth-service` | JWT `iss` claim |
| `AUTH_TOKEN_TTL_SECONDS` | `900` | access token TTL |
| `AUTH_REFRESH_TOKEN_TTL_SECONDS` | `604800` | refresh token TTL |
| `AUTH_DEV_TEST_TOKEN_ENABLED` | `false` | `/v1/internal/dev/test-token` 활성화 |
| `AUTHZ_CACHE_ENABLED` | `false` | JWT 전환 후 stale principal 위험 때문에 현재 조립 경로에서는 사용하지 않음 |

JWT 구현은 Go 규칙의 Auth 후보에 맞춰 `github.com/golang-jwt/jwt/v5`를 사용합니다. 표준 라이브러리만으로 HS256을 직접 구현할 수는 있지만, claim 검증과 expiry/issuer 검증의 실수를 줄이기 위해 검증된 JWT 라이브러리를 선택했습니다.

## 인증 API

- `POST /v1/auth/signup`
- `POST /v1/auth/login`
- `POST /v1/auth/refresh`
- `POST /v1/auth/logout`
- `POST /v1/auth/introspect`
- `POST /v1/internal/auth/sessions/{sessionId}/revoke`
- `POST /v1/internal/dev/test-token`: `AUTH_DEV_TEST_TOKEN_ENABLED=true`일 때만 사용 가능

## 운영 엔드포인트

- `GET /healthz`
- `GET /readyz`: database check 포함
- `GET /metrics`

## 남은 결정점

- `auth_user_links.user_id UNIQUE`는 다중 인증 수단/계정 연결/OAuth 확장을 막을 수 있습니다. 스키마 확장은 데이터 마이그레이션 정책과 함께 별도로 정해야 합니다.
- `JWT_SECRET`은 현재 GitOps dev 값에 평문 placeholder로 들어 있습니다. 운영 환경에서는 Secret/ExternalSecret으로 옮겨야 합니다.
