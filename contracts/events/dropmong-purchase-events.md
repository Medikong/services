# DropMong 구매 이벤트 계약

이 문서는 구매, 결제 만료, 재고 투영, 취소와 전액 환불 시나리오에서 서비스 사이에 오가는 Kafka topic과 payload 기준을 고정한다.

## Topic

| topic | producer | consumer | 용도 |
| --- | --- | --- | --- |
| `order.created` | `order-service` | `payment-service` | 결제 대상 주문을 payment-service의 `known_orders`에 등록 |
| `payment.approved` | `payment-service` | `order-service` | 결제 승인 후 주문 확정 |
| `payment.failed` | `payment-service` | `order-service` | 결제 실패 반영과 예약 수량 해제 |
| `order.confirmed` | 미구현 | 미구현 | 후속 분석·확장용 예약 계약 |
| `order.expired` | `order-service` | 구매 생명주기 소비자 | 결제 기한 만료 결과 |
| `inventory.changed` | `order-service` | `catalog-service` | 버전이 있는 절대 재고 수량 투영 |
| `refund.requested` | `order-service` | `payment-service` | 전액 환불 원장 생성 요청 |
| `refund.completed` | `payment-service` | `order-service` | 전액 환불 완료 결과 |
| `refund.failed` | `payment-service` | `order-service` | 전액 환불 실패 결과 |
| `notification.requested` | `order-service` | `notification-service` | 유형화된 구매 생명주기 알림 생성 |

## 공통 필드

| 필드 | 타입 | 필수 | 설명 |
| --- | --- | --- | --- |
| `schemaVersion` | integer | X | 생략 시 `1`; 기존 JSON과의 호환성을 유지하는 스키마 버전 |
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
| `order.expired` | `orderId`, `dropId`, `productId`, `quantity`, `amount` |
| `inventory.changed` | `dropId`, `productId`, `totalQuantity`, `reservedQuantity`, `soldQuantity`, `remainingQuantity`, `inventoryVersion` |
| `refund.requested` | `refundId`, `orderId`, `paymentId`, `amount`, `reason` |
| `refund.completed` | `refundId`, `orderId`, `paymentId`, `amount` |
| `refund.failed` | `refundId`, `orderId`, `paymentId`, `amount`, `reason` |
| `notification.requested` | `notificationId`, `orderId`, `notificationType`, `channel`, `title`, `message` |

## 현재 처리 규칙

- `payment-service`는 `order.created`를 소비해 `orderId` 기준으로 결제 대상 주문을 저장한다.
- `order-service`는 `payment.failed`의 `eventId`를 `processed_payment_events`에 기록해 중복 상태 전이를 막는다.
- `notification-service`는 `notification.requested`의 `eventId`에 unique constraint를 적용해 중복 알림 생성을 막는다.
- 결제 이벤트의 `orderId`에 해당하는 주문이 없으면 현재 consumer는 상태를 변경하지 않고 종료한다. 자동 retry와 DLQ는 아직 없다.
- `notification.requested`는 기본 `IN_APP` 채널을 사용하고 `notificationType`을 생략한 기존 payload는 `ORDER_CONFIRMED`로 해석한다.
- `notificationType`은 `ORDER_CONFIRMED`, `PAYMENT_FAILED`, `ORDER_EXPIRED`, `ORDER_CANCELED`, `PAYMENT_REFUNDED`, `REFUND_FAILED` 중 하나다.
- 모든 모델은 알 수 없는 필드를 거부하며, 기존 이벤트 payload에 `schemaVersion`이나 `notificationType`을 새 필수 필드로 요구하지 않는다.
- order-service와 payment-service는 DB commit과 Kafka publish 사이를 transactional outbox로 연결하며, relay가 성공적으로 ack된 이벤트만 발행 완료로 기록한다.
