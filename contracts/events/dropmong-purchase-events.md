# DropMong 구매 이벤트 계약

이 문서는 정상 구매, 결제 실패, 품절/동시성 시나리오에서 서비스 사이에 오가는 Kafka topic과 payload 기준을 고정한다.

## Topic

| topic | producer | consumer | 용도 |
| --- | --- | --- | --- |
| `order.created` | `order-service` | `payment-service` | 결제 대상 주문을 payment-service의 `known_orders`에 등록 |
| `payment.approved` | `payment-service` | `order-service` | 결제 승인 후 주문 확정 |
| `payment.failed` | `payment-service` | `order-service` | 결제 실패 반영과 예약 수량 해제 |
| `order.confirmed` | 미구현 | 미구현 | 후속 분석·확장용 예약 계약 |
| `notification.requested` | `order-service` | `notification-service` | 현재는 주문 확정 알림 생성 |

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

## 현재 처리 규칙

- `payment-service`는 `order.created`를 소비해 `orderId` 기준으로 결제 대상 주문을 저장한다.
- `order-service`는 `payment.failed`의 `eventId`를 `processed_payment_events`에 기록해 중복 상태 전이를 막는다.
- `notification-service`는 `notification.requested`의 `eventId`에 unique constraint를 적용해 중복 알림 생성을 막는다.
- 결제 이벤트의 `orderId`에 해당하는 주문이 없으면 현재 consumer는 상태를 변경하지 않고 종료한다. 자동 retry와 DLQ는 아직 없다.
- `notification.requested`는 기본 `IN_APP` 채널만 1차 구현 범위로 둔다.
- 결제 실패 알림, `order.confirmed` 발행, DLQ, retry backoff, schema registry는 후속 범위다.
- order-service와 payment-service의 DB commit과 Kafka publish 사이에는 아직 transactional outbox가 없다. 주문 생성 뒤 `order.created`, 결제 승인 뒤 `payment.approved`, 결제 실패 뒤 `payment.failed`, 주문 확정 뒤 `notification.requested` 발행 구간을 모두 원자화해야 한다.
