# backoffice-service

운영자용 드롭 준비와 준비 상태 확인을 담당하는 Go 서비스입니다.

드롭 준비 use case는 로컬 상품, 드롭, 재고 준비를 먼저 저장한 뒤 coupon-service에 쿠폰 정책 준비를 요청합니다. 쿠폰 정책의 최종 발급 원장은 coupon-service가 소유합니다.

## 구조

- `internal/app`: repository, coupon-service client, HTTP route wiring
- `internal/domain/drop`: 드롭 준비 모델, use case, repository port, memory/PostgreSQL repository, coupon-service HTTP client
- `internal/platform/config`: 환경 설정 로딩
- `internal/transport/http`: admin route와 operational route

## 실행

```bash
go run ./cmd/server
```

기본 주소는 `:8080`입니다. `HTTP_ADDR`로 변경할 수 있습니다. `DATABASE_URL`이 있으면 PostgreSQL repository를 사용하고, 없으면 local/dev용 memory repository를 사용합니다.

`COUPON_SERVICE_URL`로 coupon-service 내부 API 주소를 설정합니다.

## API

- `POST /admin/drops/prepare`
- `GET /admin/drops/{dropId}/readiness`
- `GET /healthz`
- `GET /readyz`
- `GET /metrics`
