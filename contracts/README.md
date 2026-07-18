# DropMong OpenAPI Contracts

이 폴더는 DropMong 서비스군의 REST API 계약 초안을 둔다. 서비스 구현 코드는 이 계약을 기준으로 독립 구현할 수 있어야 하며, 서비스 간 강한 코드 의존을 만들지 않는다.

## 범위

- `catalog-service`: 드롭 목록, 드롭 상세, 상품 공개 조회
- `order-service`: 주문 생성, 재고 예약, 주문 조회, 결제 승인 이벤트 처리
- `payment-service`: mock 결제 생성, 결제 상태 조회, 결제 이벤트 발행
- `notification-service`: 알림 목록 조회와 이벤트 기반 알림 저장
- `auth-service`: 로그인과 JWT 발급. 정상 구매 1차 구현에서는 직접 구현 대상에서 제외할 수 있다.
- `dropmong-web`: 구매자 웹과 같은 Next.js 배포 단위에서 실행하는 BFF Route Handler. 독립 마이크로서비스는 아니지만 브라우저가 호출하는 동일 출처 계약은 이 폴더에 명시한다.

페이지 UI 자체는 OpenAPI contract 대상이 아니다. 다만 웹 애플리케이션이 제공하는 BFF Route Handler는 브라우저와 서버의 경계를 명확히 하기 위해 계약으로 관리한다.

Kafka 이벤트 계약은 OpenAPI에 포함하지 않는다. 정상 구매 흐름의 topic과 payload 기준은 `events/dropmong-purchase-events.md`에 둔다.

## 폴더 구조

```text
contracts/
  README.md
  common-conventions.md
  jwt-conventions.md
  operational-endpoints.md
  common/
    components.yaml
  events/
    dropmong-purchase-events.md
  services/
    catalog-service/
      openapi.yaml
    order-service/
      openapi.yaml
    payment-service/
      openapi.yaml
    notification-service/
      openapi.yaml
    dropmong-web/
      openapi.yaml
```

각 서비스의 `openapi.yaml`은 서비스의 `info`, `servers`, `security`, `paths`, `components.schemas`를 정의한다. Path 단위 상세 요청/응답 분리는 서비스 구현이 커진 뒤 필요한 시점에 진행한다.

## 공통 규칙

- OpenAPI 버전은 `3.1.0`을 사용한다.
- 인증은 `Authorization: Bearer <JWT>`를 기본으로 한다.
- JWT/JWKS 발급·검증, Session 확인과 identity-only claim/header 규칙은 `jwt-conventions.md`를 따른다.
- 위 JWT/JWKS 규칙은 목표 계약이다. 현재 활성 Helm stack의 effective legacy `JWT_SECRET`, Kong HS256 및 role/email plugin, Notification/Interest의 legacy role-header trust와 Auth issuer fallback은 `jwt-conventions.md`에 미해결 deployment blocker로 기록하며, 후속 migration 전에는 RS256/identity-only-ready 배포로 간주하지 않는다.
- `/healthz`, `/readyz`, `/metrics` 운영 엔드포인트는 `operational-endpoints.md`를 따른다.
- ID 타입은 모두 `string`으로 둔다.
- 목록 API는 `limit`, `cursor` 기반 페이지네이션을 사용한다.
- 생성/상태 변경 API는 중복 요청 방지가 필요하면 `Idempotency-Key`를 받는다.
- 오류 응답은 `common/components.yaml`의 `ErrorResponse`와 공통 response를 참조한다.

## 서비스별 위치

- `services/catalog-service/openapi.yaml`
- `services/order-service/openapi.yaml`
- `services/payment-service/openapi.yaml`
- `services/notification-service/openapi.yaml`
- `events/dropmong-purchase-events.md`
