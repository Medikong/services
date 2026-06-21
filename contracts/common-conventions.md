# 공통 OpenAPI 규약

이 문서는 ticketing 서비스군의 REST API 계약 초안을 작성할 때 사용하는 공통 규칙이다.

## 기본 규칙

- OpenAPI 버전은 `3.1.0`을 사용한다.
- 요청과 응답의 기본 `Content-Type`은 `application/json`이다.
- 날짜와 시간은 ISO-8601 형식의 `string` + `format: date-time`을 사용한다.
- ID는 숫자 타입 대신 `string`으로 정의한다.
- 인증은 `Authorization: Bearer <JWT>`를 기본으로 한다.
- 요청 추적 헤더는 `X-Request-Id`와 `X-Trace-Id`를 사용한다.
- 중복 요청 방지가 필요한 생성/변경 API는 `Idempotency-Key` 헤더를 받는다.
- 목록 조회 페이지네이션은 `limit`, `cursor` 쿼리 파라미터를 사용한다.
- 오류 응답은 공통 `ErrorResponse` 스키마를 사용한다.
- `/healthz`, `/readyz`, `/metrics` 운영 엔드포인트는 [operational-endpoints.md](./operational-endpoints.md)를 기준으로 한다.

## JWT 규칙

JWT 발급, 검증, role, claim 규칙은 [jwt-conventions.md](./jwt-conventions.md)를 기준으로 한다.

요약:

- Access token은 `HS256` JWT로 발급한다.
- MVP에서는 모든 서비스가 같은 `JWT_SECRET`으로 access token을 검증한다.
- `Authorization` 헤더는 `Bearer <accessToken>` 형식만 허용한다.
- `role`은 `CUSTOMER`, `PROVIDER`, `ADMIN` 중 하나이다.
- 필수 claim은 `iss`, `sub`, `email`, `role`, `iat`, `exp`, `jti`이다.
- refresh token은 JWT가 아니라 opaque string이며, `auth-service`만 검증한다.

## Status Code 규칙

- `200 OK`: 조회, 취소/만료처럼 기존 리소스 상태를 반환할 때 사용한다.
- `201 Created`: 새 리소스가 만들어졌을 때 사용한다.
- `202 Accepted`: 비동기 처리로 넘긴 명령을 접수했지만 아직 완료되지 않았을 때 사용한다.
- `204 No Content`: 성공했지만 반환할 본문이 없을 때 사용한다.
- `400 Bad Request`: 요청 JSON, 쿼리, path parameter 형식이 잘못됐을 때 사용한다.
- `401 Unauthorized`: JWT가 없거나 유효하지 않을 때 사용한다.
- `403 Forbidden`: 인증은 됐지만 해당 리소스나 명령 권한이 없을 때 사용한다.
- `404 Not Found`: 리소스를 찾을 수 없을 때 사용한다.
- `409 Conflict`: 좌석 중복 선점, 이미 처리된 상태 변경처럼 현재 상태와 충돌할 때 사용한다.
- `422 Unprocessable Entity`: 형식은 맞지만 도메인 규칙상 처리할 수 없을 때 사용한다.
- `500 Internal Server Error`: 예측하지 못한 서버 오류에 사용한다.

## ErrorResponse 예시

```json
{
  "error": {
    "code": "reservation.conflict",
    "message": "Seat is already reserved.",
    "details": {
      "seatId": "2ec15d50-317a-5d4b-a686-a8bb790c08e0"
    }
  },
  "requestId": "req-01HV6W8ZK2J2J9N9S4V7T3F0CA",
  "occurredAt": "2026-05-28T10:15:30Z"
}
```

`code`는 사람이 읽는 메시지보다 안정적인 식별자 역할을 한다. 클라이언트 분기와 테스트는 `message`가 아니라 `code` 기준으로 작성한다.
