# Medikong OpenAPI Contracts

이 폴더는 ticketing 서비스군의 REST API 계약 초안을 둔다. 서비스 구현 코드는 이 계약을 기준으로 독립 구현할 수 있어야 하며, 서비스 간 강한 코드 의존을 만들지 않는다.

## 범위

- `auth-service`: 로그인과 JWT 재발급
- `concert-service`: 공연, 회차, 좌석 조회
- `reservation-service`: 좌석 선점, 예약 생성, 예약 조회/취소/만료
- `payment-service`: 결제 mock 생성과 상태 조회
- `ticket-service`: 티켓 발행과 티켓 조회
- `notification-service`: 알림 목록과 상세 조회

`dashboard`는 정적 화면이므로 OpenAPI contract 대상이 아니다.

Kafka 이벤트 계약은 OpenAPI에 포함하지 않는다. 이벤트 payload와 topic 계약은 별도 이벤트 계약 또는 AsyncAPI 후보로 분리한다.

## 폴더 구조

```text
contracts/
  README.md
  common-conventions.md
  common/
    components.yaml
  services/
    auth-service/
      openapi.yaml
      paths/
    concert-service/
      openapi.yaml
      paths/
    reservation-service/
      openapi.yaml
      paths/
    payment-service/
      openapi.yaml
      paths/
    ticket-service/
      openapi.yaml
      paths/
    notification-service/
      openapi.yaml
      paths/
```

각 서비스의 `openapi.yaml`은 서비스의 `info`, `servers`, `security`, `paths`, `components.schemas`를 정의한다. Path 단위 상세 요청/응답은 `paths/*.yaml`에 분리한다.

## 공통 규칙

- OpenAPI 버전은 `3.1.0`을 사용한다.
- 인증은 `Authorization: Bearer <JWT>`를 기본으로 한다.
- ID 타입은 모두 `string`으로 둔다.
- 목록 API는 `limit`, `cursor` 기반 페이지네이션을 사용한다.
- 생성/상태 변경 API는 중복 요청 방지가 필요하면 `Idempotency-Key`를 받는다.
- 오류 응답은 `common/components.yaml`의 `ErrorResponse`와 공통 response를 참조한다.

## 서비스별 위치

- `services/auth-service/openapi.yaml`
- `services/concert-service/openapi.yaml`
- `services/reservation-service/openapi.yaml`
- `services/payment-service/openapi.yaml`
- `services/ticket-service/openapi.yaml`
- `services/notification-service/openapi.yaml`
