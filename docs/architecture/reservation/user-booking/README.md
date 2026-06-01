# 사용자 예매 흐름

이 문서는 사용자가 공연을 조회하고 좌석을 선택한 뒤 예약, 결제, 티켓 발급, 알림까지 이어지는 흐름을 한 곳에서 보기 위한 기준 문서다.

실제 이벤트 토픽명과 payload 모델은 `packages/contracts/src/contracts/events.py`를 기준으로 한다. 이 문서는 흐름과 서비스 책임을 설명하고, 계약의 세부 필드를 중복 정의하지 않는다.

## 시퀀스

```mermaid
sequenceDiagram
    autonumber
    actor User as 사용자
    participant Auth as auth-service
    participant Concert as concert-service
    participant Reservation as reservation-service
    participant Payment as payment-service
    participant Kafka as Kafka
    participant Ticket as ticket-service
    participant Notify as notification-service

    User->>Auth: 로그인
    Auth-->>User: access token

    User->>Concert: 공연 목록 조회
    Concert-->>User: 공연 목록

    User->>Concert: 회차 조회
    Concert-->>User: 회차 목록

    User->>Concert: 좌석 조회
    Concert-->>User: 좌석 목록

    User->>Reservation: 예약 생성
    Reservation->>Reservation: 좌석 선점<br/>active_seat_key unique constraint
    Reservation-->>User: pending 예약
    Reservation-->>Kafka: reservation-created

    User->>Payment: 결제 요청
    Payment->>Payment: 결제 승인/실패 처리
    Payment-->>User: 결제 결과

    alt 결제 승인
        Payment-->>Kafka: payment-approved
        Kafka-->>Ticket: payment-approved 전달
        Ticket->>Ticket: 티켓 발급<br/>reservationId 기준 중복 방지
        Ticket-->>Kafka: ticket-issued
    else 결제 실패
        Payment-->>Kafka: payment-failed
    end

    Kafka-->>Notify: reservation/payment/ticket 이벤트 전달
    Notify->>Notify: 사용자 알림 생성

    User->>Ticket: 내 티켓 조회
    Ticket-->>User: 발급된 티켓

    User->>Notify: 내 알림 조회
    Notify-->>User: 예매/결제/티켓 알림
```

## 이벤트 흐름

```mermaid
flowchart LR
    SelectSeat["사용자 행동<br/>좌석 선택"]
    CreateReservation["Command<br/>예약 생성"]
    ReservationCreated["Event<br/>reservation-created"]
    RequestPayment["Command<br/>결제 요청"]
    PaymentApproved["Event<br/>payment-approved"]
    PaymentFailed["Event<br/>payment-failed"]
    IssueTicket["Policy<br/>결제 승인 시<br/>티켓 발급"]
    TicketIssued["Event<br/>ticket-issued"]
    NotifyReservation["Policy<br/>예약 알림 생성"]
    NotifyPayment["Policy<br/>결제 알림 생성"]
    NotifyTicket["Policy<br/>티켓 알림 생성"]

    SelectSeat --> CreateReservation
    CreateReservation --> ReservationCreated
    ReservationCreated --> NotifyReservation
    ReservationCreated --> RequestPayment
    RequestPayment --> PaymentApproved
    RequestPayment --> PaymentFailed
    PaymentApproved --> IssueTicket
    PaymentApproved --> NotifyPayment
    PaymentFailed --> NotifyPayment
    IssueTicket --> TicketIssued
    TicketIssued --> NotifyTicket

    classDef action fill:#f8fafc,stroke:#64748b,color:#0f172a
    classDef command fill:#dbeafe,stroke:#2563eb,color:#1e3a8a
    classDef event fill:#dcfce7,stroke:#16a34a,color:#14532d
    classDef policy fill:#fef3c7,stroke:#d97706,color:#78350f

    class SelectSeat action
    class CreateReservation,RequestPayment command
    class ReservationCreated,PaymentApproved,PaymentFailed,TicketIssued event
    class IssueTicket,NotifyReservation,NotifyPayment,NotifyTicket policy
```

## 이벤트 요약

| topic | producer | consumers | 목적 |
| --- | --- | --- | --- |
| `reservation-created` | `reservation-service` | `notification-service` | 예약 생성 사실을 알림으로 남긴다. |
| `reservation-expired` | `reservation-service` | `notification-service` | 결제 시간 만료로 예약이 풀렸음을 알린다. |
| `payment-approved` | `payment-service` | `ticket-service`, `notification-service` | 결제 승인 후 티켓 발급과 결제 완료 알림을 시작한다. |
| `payment-failed` | `payment-service` | `notification-service` | 결제 실패 알림을 생성한다. |
| `ticket-issued` | `ticket-service` | `notification-service` | 티켓 발급 완료 알림을 생성한다. |

## 현재 구현 메모

- `reservation-service`는 DB unique constraint로 같은 회차/좌석의 중복 active 예약을 막는다.
- `ticket-service`는 `payment-approved` 이벤트를 소비해 티켓을 발급할 수 있고, `reservationId` 기준 중복 발급을 막는다.
- `notification-service`는 예약, 결제, 티켓 이벤트를 받아 알림을 만들 수 있다.
- 전체 사용자 예매 E2E를 안정적으로 만들려면 `payment-service`의 이벤트 payload와 Kafka 발행, 그리고 E2E Compose의 결제/티켓 서비스 구성이 먼저 정렬되어야 한다.
