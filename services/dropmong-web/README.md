# DropMong Web

구매자·판매자용 반응형 Next.js 애플리케이션입니다. 현재 코드는 같은 Node.js 프로세스와 Docker 이미지 안에 buyer Route Handler와 제거 예정인 seller BFF를 함께 포함합니다. 목표 구조에서는 buyer·auth 경계만 웹 BFF에 남기고, seller 업무 API는 브라우저가 Kubernetes Ingress를 통해 실제 소유 서비스로 호출합니다.

## 상태 읽는 법

- **코드 지원**: 호출 함수나 Route Handler가 저장소에 존재합니다.
- **로컬 연결**: 현재 `.env.local`로 실제 서비스를 호출할 수 있습니다.
- **검증 연결**: CI, 브라우저 테스트 또는 Docker smoke가 실제 서비스를 사용합니다.
- **배포 연결**: GitOps에 DropMong Deployment, 환경 변수와 Ingress route가 선언되어 있습니다.

코드가 존재한다는 사실만으로 로컬·검증·배포 연결을 완료한 것으로 보지 않습니다.

## 현재 buyer 화면

| 화면 | 실제 route | 서버 함수 | Route Handler | 현재 데이터 |
| --- | --- | --- | --- | --- |
| 홈 | `/` | `getHomePage` | `GET /api/web/home` | Catalog 설정 시 실제 조회, 기본 로컬 설정은 mock |
| 상품 상세 | `/products/[productId]?dropId=...` | `getProductDetailPage` | `GET /api/web/products/[productId]` | Catalog 설정 시 실제 조회, 개인화는 미연결 |
| 주문·결제 | `/checkout?checkoutId=...` | `getCheckoutSnapshot` | `GET/POST /api/web/checkouts/[checkoutId]/**` | 개발 fixture |
| 주문 완료 | `/orders/complete?orderId=...` | `getOrderResult` | `GET /api/web/orders/[orderId]` | 개발 fixture |

Server Component는 현재 Route Handler를 다시 호출하지 않고 같은 `src/server/bff/` 함수를 직접 사용합니다. 브라우저 mutation과 주문 상태 polling은 동일 출처 Route Handler를 사용합니다.

## 서비스 연동 상태

| 영역 | 코드·API 사실 | 로컬·검증 설정 | 배포 상태 | 판정 |
| --- | --- | --- | --- | --- |
| Catalog | `catalog.ts`가 `CATALOG_INTERNAL_BASE_URL`이 있으면 `GET /drops`, `GET /drops/{dropId}`를 호출 | `.env.local`, CI, Playwright, Docker smoke에 Catalog URL이 없고 `DEV_MOCK_MODE=true` | 확인한 GitOps에 `catalog-service`, `dropmong-web` 선언 없음 | 코드 지원, 실행·배포 미연결 |
| Auth | `auth-service`에 `GET /api/v1/auth/context`와 HTTP E2E가 구현됨 | 웹 `auth.ts`와 `/api/web/auth/context`는 개발용 서명 cookie만 읽음 | GitOps의 DropMong `/auth` route와 canonical `/api/v1/auth/**`의 일치 여부 및 실제 배포는 미확인 | API 구현, 프론트 미연결 |
| Checkout snapshot·confirm | 웹 Route Handler와 보안 검사는 구현됨 | `checkout.ts`가 `DEV_MOCK_MODE=true`에서만 fixture 결과를 생성 | canonical Checkout 서비스·Ingress 미확정 | fixture |
| Order | `order-service`에 `POST /orders`, `GET /orders/{orderId}`가 구현됨 | 웹은 `dev-order.*` 식별자와 fixture만 사용 | 확인한 GitOps에 `order-service` 선언 없음 | API 존재, 프론트 미연결 |
| Payment | `payment-service`에 mock 승인·실패와 단건 조회 API가 구현됨 | 웹 호출 client 없음 | GitOps에 DropMong `/payments` route가 있으나 실제 배포는 미확인 | API 존재, 프론트 미연결 |
| 배송 | 주문 완료 화면의 상태와 예상 배송일만 표시 | `getOrderResult` fixture | 소유 서비스·API·Ingress 미확인 | fixture, 소유 미확정 |
| 쿠폰 | `coupon-service`에 구매자 보유 쿠폰 API가 구현됨 | Checkout 문구와 할인액은 fixture, 웹 client 없음 | DropMong 표기의 private-dev·`/coupons` 선언은 있으나 현재 `/api/v1/**` 계약과 경로가 달라 canonical API Ingress는 미연결, 실제 배포 미확인 | API 존재, 프론트 미연결 |
| 포인트 | 서비스 inventory와 웹 client에 원장 API 없음 | Checkout 안내 문구와 금액만 fixture | 소유 서비스·Ingress 미확인 | 소유 미확정 |
| 결제수단 | Checkout에 `MOCK_CARD`만 제공 | 실제 결제수단 조회 없음 | 소유 서비스·Ingress 미확인 | fixture, 소유 미확정 |
| User | 사용자 생성·본인 프로필 API가 구현됨 | 웹 client 없음 | DropMong 표기의 private-dev와 `/api/v1/users`, `/api/v1/users/me` Ingress 선언 있음, 실제 동기화 미확인 | API 존재, 프론트 미연결 |
| Interest | 관심·랭킹 API가 구현됨 | 웹 client 없음 | 확인한 GitOps에 `interest-service` 선언 없음 | API 존재, 프론트 미연결 |
| Notification | 구매자 알림 API가 구현됨 | 웹 client 없음 | GitOps에 DropMong `/notifications` route가 있으나 실제 배포는 미확인 | API 존재, 프론트 미연결 |

`DEV_MOCK_MODE=false`에서는 준비되지 않은 Checkout 계약을 성공처럼 대체하지 않고 `WEB_CHECKOUT_CONTRACT_UNAVAILABLE`을 반환합니다. Catalog upstream 장애도 mock으로 바꾸지 않습니다.

## Auth 경계

- 현재 `GET /api/web/auth/context`는 실제 `auth-service`를 호출하지 않습니다.
- `/api/web/auth/development-session`이 발급한 서명 HttpOnly cookie를 `getServerActor`와 `getRequestActor`가 직접 해석합니다.
- 현재 `GET /api/v1/auth/context`는 mobile credential만 허용하므로 개발용 web cookie를 그대로 보낼 수 없습니다. browser credential 계약을 정합화하거나 별도 교환 경계를 확정한 뒤 최소 session 검증 결과만 사용하며, 이메일 같은 Identity 값을 내부 header나 JWT claim으로 복제하지 않습니다.
- Auth는 seller membership·role·permission 원장이 아닙니다.

## Buyer BFF와 Seller BFF

buyer Route Handler는 화면 DTO, 웹 session·CSRF, 오류 변환과 Checkout 같은 단일 업무 계약 전달을 담당하는 목표 구조의 일부입니다. 가격·재고·쿠폰·포인트·주문·결제 원장을 직접 소유하거나 여러 Command를 순서대로 조정하지 않습니다.

현재 seller 화면은 다음 코드와 fixture에 의존합니다.

- `app/api/web/seller/[...path]/route.ts`
- `src/server/bff/seller/**`
- `src/server/bff/seller/clients/fixtures.ts`
- `SELLER_CONTEXT_INTERNAL_BASE_URL`, `SELLER_MANAGEMENT_INTERNAL_BASE_URL` placeholder

이 seller BFF는 현행 코드 기록이자 제거 대상입니다. 목표에서는 seller Server Component도 여러 서비스를 조합해 BFF처럼 동작하지 않습니다. seller 브라우저는 Ingress-facing seller 전용 계약이 준비된 실제 소유 서비스만 호출하며, buyer API를 seller API처럼 재사용하지 않습니다.

## 검증 범위

현재 자동 검증은 mock 회귀만 확인합니다.

| 검증 | 설정 | 확인하는 것 | 확인하지 않는 것 |
| --- | --- | --- | --- |
| GitHub Actions | `DEV_MOCK_MODE=true`, Catalog URL 없음 | lint, typecheck, unit test, build, 브라우저 구매 시나리오 | Catalog·Auth·Order·Payment 실연동 |
| Playwright | `DEV_MOCK_MODE=true`, seller portal 활성화 | 모바일·데스크톱 buyer/seller fixture 화면 | 실제 downstream·Ingress |
| Docker smoke | `DEV_MOCK_MODE=true` | 이미지 시작, readiness, health, metrics, 요청 로그 | Catalog·Auth 실연동과 배포 route |

실제 Catalog 연결을 완료로 판정하려면 별도 환경에서 `CATALOG_INTERNAL_BASE_URL`을 주입하고 mock 없이 홈·상품 상세 계약과 오류 처리를 검증해야 합니다.

## 실행

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

모든 BFF 응답은 `X-Request-Id`를 반환합니다. JSON 로그에는 요청 식별자, route template, 상태, 처리 시간과 downstream 결과만 기록하며 cookie, token, CSRF, 주소, 결제 정보와 본문은 기록하지 않습니다.
