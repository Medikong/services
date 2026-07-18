# 공통 OpenAPI 규약

이 문서는 DropMong 서비스군의 REST API 계약 초안을 작성할 때 사용하는 공통 규칙이다.

## 기본 규칙

- OpenAPI 버전은 `3.1.0`을 사용한다.
- 요청과 응답의 기본 `Content-Type`은 `application/json`이다.
- 날짜와 시간은 ISO-8601 형식의 `string` + `format: date-time`을 사용한다.
- ID는 숫자 타입 대신 `string`으로 정의한다.
- 인증은 `Authorization: Bearer <JWT>`를 기본으로 한다.
- 요청 추적 헤더는 `X-Request-Id`와 W3C `traceparent`를 사용한다.
- 중복 요청 방지가 필요한 생성/변경 API는 `Idempotency-Key` 헤더를 받는다.
- 목록 조회 페이지네이션은 `limit`, `cursor` 쿼리 파라미터를 사용한다.
- 오류 응답은 공통 `ErrorResponse` 스키마를 사용한다.
- `/healthz`, `/readyz`, `/metrics` 운영 엔드포인트는 [operational-endpoints.md](./operational-endpoints.md)를 기준으로 한다.

## JWT 규칙

JWT/JWKS 발급·검증, Session 확인과 identity-only claim/header 규칙은 [jwt-conventions.md](./jwt-conventions.md)를 기준으로 한다.

요약:

- 아래 항목은 승인된 목표 계약이다. 현재 stage worktree의 Auth 소스는 RS256/JWKS와 내부 `/internal/ext-authz` Handler/router 등록을 구현했지만, issuer 미설정 시 `ServiceName`으로 fallback한다. 활성 private-dev/aws-dev Helm stack은 목록 대체 후 각각 legacy `JWT_SECRET` 하나만 유효하고 RS256 key material을 공급하지 않는다. 공통 `AUTH_JWT_SECRET`은 base 파일 선언이지만 두 active effective stack 입력은 아니며 repository dev overlay도 두 Application에서 참조하지 않는다. Kong의 HS256 credential과 role/email identity/role-guard plugin, Notification/Interest의 `X-User-Role` trust도 남아 있고 Handler는 배포되거나 Istio에 연결되지 않았다. 후속 Auth/GitOps/서비스 migration 전에는 현재 배포를 RS256/ext_authz-ready나 identity-only-ready로 보지 않는다.
- Access token protected header는 `alg=RS256`, JWKS와 일치하는 `kid`, `typ=JWT`를 사용한다.
- Istio와 Auth의 내부 adapter가 `GET /.well-known/jwks.json`의 공개키로 access token을 검증한다.
- `Authorization` 헤더는 `Bearer <accessToken>` 형식만 허용한다.
- 필수 claim allowlist는 `iss`, `sub`, `sid`, `aud`, `iat`, `exp`, `jti`이다.
- role, permission, email과 업무 ACL은 access token claim이나 내부 인증 헤더에 넣지 않는다.
- stage worktree에 구현된 내부 경로 `/internal/ext-authz`는 Session 상태를 확인하며 공개 OpenAPI나 Gateway Route에 등록하지 않는다. 보호 Route와의 Istio 연결은 후속 GitOps 작업이다.
- 인증 성공 시 `X-User-Id`, `X-Session-Id`, `X-Token-Id`만 업무 서비스에 전달한다.
- refresh token은 JWT가 아니라 opaque string이며, `auth-service`만 검증한다.

현재 배포 blocker의 원장과 해소 owner는 [jwt-conventions.md](./jwt-conventions.md)의 `현재 구현·배포와 목표의 차이` 표를 따른다. 이 문서는 active values나 Gateway manifest가 이미 전환됐다고 주장하지 않는다.

## Status Code 규칙

- `200 OK`: 조회, 취소/만료처럼 기존 리소스 상태를 반환할 때 사용한다.
- `201 Created`: 새 리소스가 만들어졌을 때 사용한다.
- `202 Accepted`: 비동기 처리로 넘긴 명령을 접수했지만 아직 완료되지 않았을 때 사용한다.
- `204 No Content`: 성공했지만 반환할 본문이 없을 때 사용한다.
- `400 Bad Request`: 요청 JSON, 쿼리, path parameter 형식이 잘못됐을 때 사용한다.
- `401 Unauthorized`: JWT가 없거나 유효하지 않을 때 사용한다.
- `403 Forbidden`: 인증은 됐지만 업무 서비스의 리소스 소유권 검사가 실패할 때 사용한다.
- `404 Not Found`: 리소스를 찾을 수 없을 때 사용한다.
- `409 Conflict`: 품절, 중복 주문, 이미 처리된 상태 변경처럼 현재 상태와 충돌할 때 사용한다.
- `422 Unprocessable Entity`: 형식은 맞지만 도메인 규칙상 처리할 수 없을 때 사용한다.
- `500 Internal Server Error`: 예측하지 못한 서버 오류에 사용한다.
- `503 Service Unavailable`: Auth/JWKS/Redis/PostgreSQL 상태를 확정할 수 없어 인증을 fail closed할 때 사용한다.

## ErrorResponse 예시

```json
{
  "error": {
    "code": "order.sold_out",
    "message": "Drop inventory is sold out.",
    "details": {
      "dropId": "drop-001"
    }
  },
  "requestId": "req-01HV6W8ZK2J2J9N9S4V7T3F0CA",
  "occurredAt": "2026-05-28T10:15:30Z"
}
```

`code`는 사람이 읽는 메시지보다 안정적인 식별자 역할을 한다. 클라이언트 분기와 테스트는 `message`가 아니라 `code` 기준으로 작성한다.
