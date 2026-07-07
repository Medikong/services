# DropMong 정상 구매 이벤트 계약

이 문서는 정상 구매 시나리오에서 서비스 사이에 오가는 Kafka topic과 payload 기준을 고정한다.

## Topic

| topic | producer | consumer | 용도 |
| --- | --- | --- | --- |
| `order.created` | `order-service` | 관측/분석 후보 | 주문 생성 사실 기록 |
| `payment.approved` | `payment-service` | `order-service` | 결제 승인 후 주문 확정 |
| `payment.failed` | `payment-service` | `order-service`, `notification-service` | 결제 실패 처리 |
| `order.confirmed` | `order-service` | 관측/분석 후보 | 주문 확정 사실 기록 |
| `notification.requested` | `order-service` | `notification-service` | 주문 확정 알림 생성 |

## 공통 필드

| 필드 | 타입 | 필수 | 설명 |
| --- | --- | --- | --- |
| `eventId` | string | O | 이벤트 고유 id |
| `eventType` | string | O | topic과 같은 이벤트 타입 |
| `userId` | string | O | 구매 사용자 id |
| `sourceId` | string | O | 이벤트를 발생시킨 aggregate id |
| `occurredAt` | date-time string | O | UTC 발생 시각 |
| `producer` | string | O | 이벤트 발행 서비스 |
| `correlationId` | string | X | 요청/이벤트 추적 id |

## 이벤트별 필드

| eventType | 추가 필드 |
| --- | --- |
| `order.created` | `orderId`, `dropId`, `productId`, `quantity`, `amount`, `idempotencyKey?` |
| `payment.approved` | `orderId`, `paymentId`, `amount` |
| `payment.failed` | `orderId`, `paymentId`, `amount`, `reason?` |
| `order.confirmed` | `orderId`, `paymentId`, `dropId`, `productId`, `quantity`, `amount` |
| `notification.requested` | `notificationId`, `orderId`, `channel`, `title`, `message` |

## 처리 규칙

- consumer는 `eventId` 기준으로 중복 처리를 막는다.
- 결제 승인 이벤트가 먼저 도착해도 `orderId`로 주문을 찾을 수 없으면 재처리 가능한 실패로 둔다.
- `notification.requested`는 기본 `IN_APP` 채널만 1차 구현 범위로 둔다.
- DLQ, retry backoff, schema registry는 구현 이후 성능/운영 Task에서 별도 확장한다.
