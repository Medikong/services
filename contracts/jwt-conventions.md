# JWT 규칙

이 문서는 DropMong 서비스군이 공통으로 사용하는 JWT 발급, 검증, role, claim 규칙을 정의한다. 다른 서비스는 이 문서를 기준으로 인증과 권한 검사를 구현한다.

## 결정 사항

| 항목 | 결정 |
| --- | --- |
| Token type | Access token은 JWT, refresh token은 opaque string |
| Access token algorithm | `HS256` |
| MVP signing key | `JWT_SECRET` |
| Authorization header | `Authorization: Bearer <accessToken>` |
| Issuer | `auth-service` |
| Role enum | `CUSTOMER`, `OPERATOR`, `ADMIN` |
| Claim schema | `common/components.yaml#/components/schemas/JwtAccessTokenClaims` |

로컬과 서비스 단위 테스트에서는 모든 서비스가 같은 `JWT_SECRET`으로 access token을 검증할 수 있다. 목표 ingress 구조에서는 Istio Gateway가 JWT를 먼저 검증하고, 서비스는 검증된 claim에서 생성된 사용자 context header를 신뢰한다. 운영형 구조가 필요해지면 `RS256`과 public key 또는 JWKS 기반 검증으로 확장한다.

## Role 정의

| Role | 의미 | 주요 접근 범위 |
| --- | --- | --- |
| `CUSTOMER` | 일반 구매 사용자 | 드롭 조회, 주문, 결제, 내 주문/알림 조회 |
| `OPERATOR` | 드롭 운영자 | 상품과 드롭 준비, 판매 상태 확인, 장애 대응 |
| `ADMIN` | 플랫폼 관리자 | 운영 정책, 권한 관리, 장애 복구, 감사 조회 |

API path는 역할에 따라 다음 기준으로 나눈다.

- 일반 사용자 API는 공개 resource path를 사용한다. 예: `/drops`, `/orders`, `/payments`, `/notifications`
- 드롭 운영자 API는 `/operator/...` prefix를 사용한다.
- 플랫폼 운영자 API는 `/admin/...` prefix를 사용한다.

역할 구분은 `auth-service`가 발급한 JWT의 `role` claim으로 처리한다.

## Access token claim

Access token은 다음 claim을 반드시 포함한다.

```json
{
  "iss": "auth-service",
  "sub": "1",
  "role": "CUSTOMER",
  "iat": 1779945000,
  "exp": 1779945900,
  "jti": "018fdc02-f2d2-7b3a-9d11-2e9f22fef001"
}
```

| Claim | 필수 | 설명 |
| --- | --- | --- |
| `iss` | Yes | 토큰 발급자. 기본값은 `auth-service`이다. |
| `sub` | Yes | 인증 사용자 id를 문자열로 넣는다. |
| `email` | No | 로그인 식별자이다. Gateway가 `X-User-Email`을 만들 때 사용할 수 있다. |
| `role` | Yes | `CUSTOMER`, `OPERATOR`, `ADMIN` 중 하나이다. |
| `iat` | Yes | 발급 시각 Unix epoch seconds이다. |
| `exp` | Yes | 만료 시각 Unix epoch seconds이다. |
| `jti` | Yes | access token 폐기와 추적에 사용하는 token id이다. |

서비스 구현은 `sub`와 `role`을 우선 신뢰한다. `customerId`, `operatorId` 같은 도메인별 id claim은 MVP 필수 claim에 포함하지 않는다. 필요한 서비스는 자체 DB에서 `sub`를 기준으로 도메인 리소스 소유권을 확인한다.

## Refresh token 규칙

Refresh token은 JWT가 아니다. `auth-service`가 발급하는 opaque string이며, 서버는 refresh token 원문을 저장하지 않고 hash를 저장한다.

- refresh token은 `POST /auth/refresh`에서만 사용한다.
- refresh 성공 시 기존 refresh token은 폐기하고 새 access token과 새 refresh token을 발급한다.
- 이미 사용했거나 폐기된 refresh token은 `401 Unauthorized`를 반환한다.
- 다른 서비스는 refresh token을 검증하거나 저장하지 않는다.

## 서비스별 검증 규칙

로컬/테스트에서 보호 API를 가진 서비스가 access token을 직접 검증하는 경우 다음 순서를 따른다.

1. `Authorization` header가 있는지 확인한다.
2. header 값이 `Bearer <token>` 형식인지 확인한다.
3. `JWT_SECRET`으로 HS256 signature를 검증한다.
4. `exp`가 현재 시각보다 이후인지 확인한다.
5. `iss`가 `auth-service`인지 확인한다.
6. `role`이 허용된 enum인지 확인한다.
7. API 권한에 필요한 role인지 확인한다.

목표 ingress에서는 Istio Gateway가 검증된 claim을 다음 header로 전달한다.

| Header | Claim | 설명 |
| --- | --- | --- |
| `X-User-Id` | `sub` | 인증 사용자 id |
| `X-User-Email` | `email` | 감사 로그와 운영 조회용 이메일 |
| `X-User-Role` | `role` | `CUSTOMER`, `OPERATOR`, `ADMIN` 권한 분기 |

서비스는 외부 클라이언트가 직접 보낸 `X-User-*` 값을 신뢰하지 않는다. Gateway 또는 테스트 harness가 검증된 claim에서 만든 값만 내부 사용자 context로 사용한다.

현재 Go 기반 `auth-service`, `user-service`, `coupon-service`, `backoffice-service`는 테스트와 내부 호출에서 `X-Principal` 헤더도 사용한다. `X-Principal`은 `userId`, `roles`, `authMethods` 등을 담은 principal JSON을 base64url로 인코딩한 내부 헤더다. 이 헤더 역시 외부 클라이언트 입력을 직접 신뢰하지 않고, Gateway 또는 신뢰된 테스트 harness가 만든 값만 사용한다.

권한이 필요한 API는 다음 status code를 사용한다.

- token이 없거나 유효하지 않으면 `401 Unauthorized`
- token은 유효하지만 role 권한이 없으면 `403 Forbidden`

## ErrorResponse 기준

인증 실패와 권한 실패도 공통 `ErrorResponse`를 사용한다.

```json
{
  "error": {
    "code": "auth.invalid_token",
    "message": "Missing or invalid bearer token."
  },
  "requestId": "req-01HV6W8ZK2J2J9N9S4V7T3F0CA",
  "occurredAt": "2026-05-28T10:15:30Z"
}
```

권장 error code는 다음과 같다.

| Code | Status | 의미 |
| --- | --- | --- |
| `auth.missing_token` | 401 | Authorization header가 없다. |
| `auth.invalid_authorization_header` | 401 | Bearer 형식이 아니다. |
| `auth.invalid_token` | 401 | token 구조, 서명, claim이 유효하지 않다. |
| `auth.token_expired` | 401 | access token이 만료됐다. |
| `auth.token_revoked` | 401 | access token이 폐기됐다. |
| `auth.invalid_refresh_token` | 401 | refresh token이 없거나 유효하지 않다. |
| `auth.forbidden` | 403 | 인증은 됐지만 해당 API 접근 권한이 없다. |

## 기존 의료 도메인 계약과의 차이

기존 auth-service 계약에는 의료 도메인의 role과 claim이 포함되어 있었다.

| 기존 | 변경 |
| --- | --- |
| `STAFF`, `PATIENT`, `DOCTOR` | `CUSTOMER`, `PROVIDER`, `ADMIN` |
| role별 secret: `STAFF_JWT_SECRET`, `PATIENT_JWT_SECRET`, `DOCTOR_JWT_SECRET` | 단일 secret: `JWT_SECRET` |
| role별 issuer: `staff`, `patient`, `doctor` | 단일 issuer: `auth-service` |
| `patientId`, `doctorId` claim | 제거 |

이 변경은 DropMong 커머스 도메인의 구매 사용자, 드롭 운영자, 플랫폼 관리자 액터를 기준으로 인증 계약을 다시 맞추기 위한 것이다.
