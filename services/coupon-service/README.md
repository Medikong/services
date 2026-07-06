# coupon-service

쿠폰 정책 준비, 발급, 사용자별 발급 조회를 담당하는 Go 서비스입니다.

PostgreSQL은 쿠폰 발급 원장입니다. Redis gate는 중복 요청과 sold-out 폭주를 DB 앞단에서 줄이는 admission layer이며, 최종 발급 상태를 대체하지 않습니다.

## 구조

- `internal/app`: repository, Redis gate, metrics, HTTP route wiring
- `internal/domain/coupon`: 쿠폰 모델, use case, repository port, memory/PostgreSQL repository, Redis admission gate
- `internal/platform/config`: 환경 설정 로딩
- `internal/transport/http`: internal policy route, public coupon route, operational/metrics route

## 실행

```bash
go run ./cmd/server
```

기본 주소는 `:8080`입니다. `HTTP_ADDR`로 변경할 수 있습니다. `DATABASE_URL`이 있으면 PostgreSQL repository를 사용하고, 없으면 local/dev용 memory repository를 사용합니다.

Redis gate는 `COUPON_REDIS_GATE_ENABLED=true`와 `REDIS_URL`을 함께 설정할 때 활성화됩니다.

## API

- `POST /internal/coupon-policies`
- `GET /internal/coupon-policies/{policyId}`
- `POST /coupons/issue`
- `GET /coupons/me`
- `GET /healthz`
- `GET /readyz`
- `GET /metrics`
