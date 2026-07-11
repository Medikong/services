# DropMong Web

구매자용 반응형 Next.js 웹 애플리케이션과 애플리케이션 내부 BFF입니다. BFF는 같은 Node.js 프로세스와 Docker 이미지에서 Route Handler와 `src/server/bff/` 모듈로 실행되며, 별도 서비스나 데이터베이스를 만들지 않습니다.

## 첫 세로 단위

- `/` - 공개 홈과 드롭 탐색
- `/products/[productId]?dropId=...` - 공개 상품 상세와 구매 시작
- `/checkout?checkoutId=...` - 서버 checkout snapshot과 결제 요청
- `/orders/complete?orderId=...` - 주문 결과 확인

`dropId`, `checkoutId`, `orderId`는 브라우저 전역 상태가 아니라 URL을 통해 복원합니다. 서버는 URL 값을 신뢰하지 않고 Catalog 또는 checkout 원장을 다시 조회해야 합니다.

## 실제 연동과 개발 mock

| 영역 | 현재 처리 | 근거 |
| --- | --- | --- |
| 드롭·상품 조회 | `CATALOG_INTERNAL_BASE_URL`이 있으면 실제 `catalog-service` 계약 호출 | `GET /drops`, `GET /drops/{dropId}` |
| 인증 session | 개발 전용 서명 HttpOnly cookie mock | Auth context OpenAPI는 있으나 실행 가능한 API가 아직 없음 |
| checkout snapshot·confirm | 개발 전용 mock | 통합 checkout 계약이 아직 없음 |
| 주문·결제 직접 호출 | 하지 않음 | BFF가 재고·주문·결제를 순차 조정하면 안 됨 |
| 배송·쿠폰·포인트·결제수단 | 개발 전용 표시 fixture | 해당 Query/Command 계약이 아직 없음 |

`DEV_MOCK_MODE=false`에서는 준비되지 않은 checkout 계약을 성공처럼 대체하지 않고 `WEB_CHECKOUT_CONTRACT_UNAVAILABLE` 오류로 처리합니다. Catalog upstream 장애도 mock으로 바꾸지 않습니다.

## 실행과 검증

```bash
cp .env.example .env.local
corepack pnpm install --frozen-lockfile
corepack pnpm lint
corepack pnpm typecheck
corepack pnpm test
corepack pnpm build
corepack pnpm test:e2e:install
corepack pnpm test:e2e
```

Docker 이미지는 저장소 루트에서 빌드합니다.

```bash
docker build -f services/dropmong-web/Dockerfile -t dropmong-web:local .
docker run --rm -p 3000:3000 \
  -e DEV_MOCK_MODE=true \
  -e APP_ORIGIN=http://localhost:3000 \
  -e SESSION_COOKIE_SECRET=replace-with-a-unique-secret-of-at-least-32-characters \
  dropmong-web:local
```

## 운영 엔드포인트

- `GET /healthz`: 프로세스 생존 상태
- `GET /readyz`: 로컬 설정과 요청 수락 상태
- `GET /metrics`: Prometheus text metrics

모든 BFF 응답은 `X-Request-Id`를 반환하며, JSON 로그에는 요청 식별자, route template, 상태, 시간과 downstream 결과만 기록합니다. cookie, token, CSRF, 주소, 결제 정보와 본문은 기록하지 않습니다.
