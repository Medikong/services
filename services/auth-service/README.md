# auth-service

인증 계정, 인증 수단, 세션, role 부여를 담당하는 Go 서비스입니다.

auth-service는 `user_id`와 인증/세션/권한만 다룹니다. 사용자 프로필, 실명, 닉네임처럼 "사용자가 누구인지"를 설명하는 정보는 user-service 책임입니다.

## 구조

- `internal/account`: auth account 생성, 이메일 가입/로그인 오케스트레이션
- `internal/credential`: password credential 저장과 이메일 credential 조회
- `internal/userlink`: `auth_account_id -> user_id` 연결
- `internal/session`: access/refresh session, refresh rotation, revoke, introspection
- `internal/rolegrant`: account role grant 저장과 조회
- `internal/principal`: Principal 생성과 authz cache
- `internal/dev`: local E2E용 deterministic test token 발급
- `internal/http`: public auth, session, introspection, dev, operational route

## 실행

```bash
DATABASE_URL=postgres://app:app@localhost:5432/auth_service?sslmode=disable \
go run ./cmd/server
```

기본 주소는 `:8080`입니다. `HTTP_ADDR`로 변경할 수 있습니다. `DATABASE_URL`이 없으면 인메모리 fallback 없이 초기화 오류로 실패합니다.

## 인증 API

- `POST /auth/signup`
- `POST /auth/login`
- `POST /auth/refresh`
- `POST /auth/logout`
- `POST /auth/introspect`
- `POST /internal/auth/sessions/{sessionId}/revoke`
- `POST /internal/dev/test-token`

## 운영 엔드포인트

- `GET /healthz`
- `GET /readyz`: database check 포함
- `GET /metrics`

## 남은 결정점

- `contracts/jwt-conventions.md`는 access token을 HS256 JWT로 정의하지만 현재 구현은 `atk_...` opaque token과 DB introspection을 사용합니다. JWT 발급/검증으로 맞출지, 계약을 opaque token/introspection 기준으로 바꿀지 결정이 필요합니다.
- role 계약 문서는 `CUSTOMER/PROVIDER/ADMIN`을 사용하지만 현재 코드의 rbac 값은 `customer/seller/operator`입니다. 구조 리팩토링에서는 값 변경을 하지 않았고, role 명명 정리가 별도 결정으로 남아 있습니다.
- `auth_user_links.user_id UNIQUE`는 다중 인증 수단/계정 연결/OAuth 확장을 막을 수 있습니다. 스키마 확장은 데이터 마이그레이션 정책과 함께 별도로 정해야 합니다.
- authz cache는 프로세스 로컬 map입니다. TTL, role 변경 무효화, 다중 pod 동기화 정책이 아직 필요합니다.
