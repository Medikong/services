# JWT/JWKS 인증 규칙

이 문서는 DropMong 보호 Route의 identity-only 인증 계약을 정의한다. Auth는 access JWT와 Session 상태만 증명하고, role, permission, 이메일이나 업무 리소스 접근 권한을 증명하지 않는다.

> 구현 상태: 이 문서는 승인된 목표 계약이다. 현재 stage worktree의 Auth 소스는 RS256/JWKS와 `/internal/ext-authz` Handler 및 router 등록을 구현했다. 이 내부 경로는 공개 OpenAPI에 없으며, 활성 GitOps desired state에도 배포·외부 route·Istio 연결이 없다. 활성 private-dev/aws-dev Helm stack은 각각 환경 overlay가 `container.env` 목록 전체를 대체하므로 legacy `JWT_SECRET` 하나만 유효하고, 공통 values의 `AUTH_JWT_SECRET`은 base 선언이지만 유효 환경 입력에는 남지 않는다. 활성 Kong은 HS256 credential뿐 아니라 role/email claim을 헤더와 인가 결정에 사용하는 plugin도 유지한다. 또한 Auth 코드는 `AUTH_JWT_ISSUER`가 없으면 `ServiceName`으로 fallback한다. 따라서 현재 배포는 RS256/ext_authz-ready나 identity-only-ready가 아니며, issuer 명시 강제와 후속 GitOps/Istio 배포·연결 전에는 보호 Route 성공 경로가 존재한다고 간주하지 않는다.

## 현재 구현·배포와 목표의 차이

| 구분 | 현재 상태 | 승인된 목표 | 해소 owner |
| --- | --- | --- | --- |
| Auth 코드의 signing/JWKS | RS256 signing과 `GET /.well-known/jwks.json` 구현 | 유지 | Auth runtime |
| issuer 설정 | `AUTH_JWT_ISSUER`가 없으면 `ServiceName`(`auth-service`)으로 fallback하며 operational mode도 이를 거부하지 않음 | 환경별 issuer를 명시적으로 공급하고 운영 fallback을 허용하지 않음 | 후속 Auth runtime/config 작업 |
| 활성 Auth values | 공통 `values/services/auth.yaml`에는 `AUTH_JWT_SECRET` 선언이 있으나, private-dev/aws-dev 환경 overlay의 `container.env` 목록이 이를 대체한다. 따라서 두 active effective stack에는 각각 `JWT_SECRET` 하나만 남고 최신 RS256 입력은 없다. `values/services/dev/auth.yaml`은 두 active Application에서 참조하지 않는다. | legacy 목록을 제거하고 private key, key ID, issuer를 승인된 Secret-backed 경로로 공급 | 후속 GitOps Auth migration |
| 활성 Gateway | `platform/kong`이 concrete HS256 JWT credential을 render | Istio RS256/JWKS 검증과 HTTP ext_authz | 후속 Gateway/GitOps migration |
| 활성 Gateway identity/role | `ticketing-identity-headers`가 `email`/`role` claim을 읽어 `X-User-Email`/`X-User-Role`을 만들고, `ticketing-role-*`가 JWT role claim으로 `403` 인가 결정을 내린다. Notification, Interest, Order, Payment ingress에 관련 plugin attachment가 남아 있다. | 세 identity header만 재생성하고 role/email 기반 Gateway 인가를 제거 | 후속 Gateway/GitOps migration |
| 업무 runtime의 legacy trust | Notification과 Interest runtime이 `X-User-Role`을 입력으로 받아 role 기반 `403`을 결정한다. | `X-User-Id`와 업무 소유 데이터로 인가하고 role/email header를 신뢰하지 않음 | 후속 Notification/Interest 서비스 migration |
| Session check | stage worktree 소스에 `/internal/ext-authz` Handler와 router 등록 구현; 공개 OpenAPI와 활성 GitOps/Istio 연결 없음 | 내부 check를 배포하고 세 identity header만 허용 | 후속 GitOps/Istio 작업 |

이 차이가 해소되기 전에는 현재 GitOps 구성을 최신 Auth 코드에 적용하거나 RS256/ext_authz 배포가 준비됐다고 선언하면 안 된다. T2는 문서/스키마 계약만 정렬하며 active values나 Kong/Istio manifest를 변경하지 않는다.

## Access JWT

| 항목 | 계약 |
| --- | --- |
| Token type | Access token은 JWT, refresh token은 opaque string |
| Protected header | `alg=RS256`, JWKS와 일치하는 `kid`, `typ=JWT` |
| Claim allowlist | `iss`, `sub`, `sid`, `aud`, `iat`, `exp`, `jti` |
| Audience | 초기 보호 API는 `dropmong-api` |
| Authorization header | `Authorization: Bearer <access-token>` |
| Claim schema | `common/components.yaml#/components/schemas/JwtAccessTokenClaims` |

Access JWT는 다음 형태만 허용한다.

```json
{
  "iss": "https://auth.example.internal",
  "sub": "018fdc02-f2d2-7b3a-9d11-2e9f22fef001",
  "sid": "018fdc02-f2d2-7b3a-9d11-2e9f22fef002",
  "aud": "dropmong-api",
  "iat": 1779945000,
  "exp": 1779945900,
  "jti": "018fdc02-f2d2-7b3a-9d11-2e9f22fef003"
}
```

| Claim | 의미 | 검증 |
| --- | --- | --- |
| `iss` | Auth issuer | 현재 코드는 설정이 없으면 `ServiceName`으로 fallback한다. 목표 운영 정책은 환경별 단일값을 명시적으로 공급하고 fallback을 거부하는 것이다. |
| `sub` | User 서비스가 만든 사용자 ID | 비어 있지 않고 Session의 `user_id`와 같아야 한다. |
| `sid` | Auth Session ID | active Session을 가리켜야 한다. |
| `aud` | 보호 API audience | `dropmong-api` 문자열 또는 이를 포함한 배열이어야 한다. |
| `iat` | 발급 시각 | 허용 clock skew를 넘는 미래 시각을 거부한다. |
| `exp` | 만료 시각 | 현재보다 이후이며 `iat`보다 커야 한다. |
| `jti` | access token ID | 비어 있지 않아야 한다. |

allowlist 밖의 claim은 발급하지 않는다. 특히 `role`, `roles`, `permission`, `permissions`, `email`, 전화번호, 이름, 프로필, membership과 업무 ACL을 access JWT에 넣지 않는다. access token 원문도 로그, trace, metric label, 감사 이벤트 또는 저장소에 남기지 않는다.

## JWKS

Auth는 `GET /.well-known/jwks.json`에서 RS256 공개키를 JWK Set으로 제공한다. 각 JWK의 `kid`는 JWT protected header의 `kid`와 정확히 연결되어야 한다.

- 응답은 `200 application/json`이며 `Cache-Control`과 `ETag`를 제공한다.
- rotation 중에는 신규 서명키와 아직 유효한 JWT를 검증할 retiring 공개키를 함께 제공할 수 있다.
- private key, key manager 참조, 내부 `active`/`retiring` 상태와 운영 메타데이터는 공개하지 않는다.
- 검증자가 `kid`를 찾지 못하면 JWKS를 제한적으로 새로 가져온 뒤에도 없을 때 `401`로 거부한다.
- 사용할 수 있는 검증된 cache가 없고 JWKS 상태를 확정할 수 없으면 보호 Route를 fail closed로 `503` 처리한다.

JWKS Route는 브라우저용 업무 API가 아니다. 별도 사용자 credential 없이 허용된 Ingress/mesh 구성요소만 접근하도록 network policy로 제한한다.

## Route 분류

Route는 prefix 추측이 아니라 배포 manifest의 명시적 allowlist로 분류한다.

| Route 종류 | JWT/Session 규칙 | 상태 소유자 |
| --- | --- | --- |
| 가입, 로그인, 비밀번호 재설정 같은 공개 Auth Route | Bearer JWT와 Session 상태 검사를 적용하지 않고 각 Auth flow credential을 검증한다. | Auth |
| 업무 보호 Route와 `GET /api/v1/auth/context` | Istio JWT 검증과 `/internal/ext-authz` Session 확인이 모두 필수다. | Istio + Auth |
| `POST /api/v1/auth/sessions/refresh`, `logout` | Bearer JWT를 요구하지 않고 refresh cookie/header credential을 Auth에 전달한다. | Auth |
| `GET /.well-known/jwks.json` | 사용자 JWT/Session 검사를 적용하지 않는다. | Auth + mesh network policy |
| `/internal/ext-authz` | 외부 Gateway Route에 등록하지 않고 지정된 Ingress workload identity만 호출한다. | Auth internal adapter |

## 보호 Route와 HTTP ext_authz

보호 Route의 목표 처리 순서는 다음과 같다.

1. Istio Ingress가 외부 요청의 `X-User-*`, `X-Session-*`, `X-Token-*` 헤더를 제거한다.
2. Istio가 RS256, `kid`, `typ`, signature와 필수 claim을 검증한다. Bearer JWT가 없거나 유효하지 않으면 `401`로 종료한다.
3. Istio HTTP ext_authz가 request body 없이 `Authorization`, `X-Request-Id`, 원래 method/path만 Auth의 `/internal/ext-authz`로 전달한다.
4. Auth adapter가 같은 기준으로 JWT를 다시 검증하고 `sub`, `sid`, `jti`만 Session 상태 검사에 사용한다.
5. 공유 Redis cache와 PostgreSQL 원장으로 active 상태와 `sub == session.user_id`를 확정한 경우에만 `200`으로 허용한다.
6. Istio가 허용 응답의 세 헤더만 덮어써서 업무 서비스로 전달한다.

| ext_authz 결과 | HTTP | 출력 헤더 | 의미 |
| --- | --- | --- | --- |
| allow | `200` | `X-User-Id`, `X-Session-Id`, `X-Token-Id` | JWT와 Session identity가 유효하다. |
| authentication deny | `401` | 없음 | JWT 오류, Session expired/revoked 또는 `sub` 불일치다. |
| indeterminate | `503` | 없음 | Auth/JWKS/Redis/PostgreSQL 상태를 확정할 수 없다. |

초기 timeout은 200ms이며 자동 재시도와 fail-open을 허용하지 않는다. `/internal/ext-authz`는 공개 OpenAPI `paths`에 포함하지 않는다.

### 내부 인증 헤더 allowlist

| Header | 원천 | 의미 |
| --- | --- | --- |
| `X-User-Id` | `jwt.sub` | 인증된 사용자 ID |
| `X-Session-Id` | `jwt.sid` | 인증에 사용한 Session ID |
| `X-Token-Id` | `jwt.jti` | 요청에 사용한 access JWT ID |

위 세 헤더만 ext_authz 성공 출력으로 허용한다. 이메일, role, permission, membership, Identity 세부 속성이나 업무 ACL 헤더는 만들지 않는다. 외부에서 받은 동일 이름 헤더를 보존하거나 병합하지 않는다.

업무 서비스는 JWT를 parsing하거나 JWKS, Redis, Auth PostgreSQL을 직접 조회하지 않는다. 대신 `X-User-Id`를 도메인 리소스 소유자와 비교한다. 인증 이후 주문, 결제 등 리소스 소유권이 맞지 않으면 해당 업무 서비스가 `403 Forbidden`을 반환한다. 이 `403`은 Istio/Auth의 `401` 인증 거부와 합치지 않는다.

## Refresh token

Refresh token은 JWT가 아닌 opaque 값이며 Auth만 검증한다. 서버는 원문 대신 keyed hash를 저장한다.

- `POST /api/v1/auth/sessions/refresh` 성공 시 기존 credential을 폐기하고 새 access/refresh credential을 발급한다.
- 이미 사용했거나 폐기된 refresh credential은 `401`을 반환하고 정책에 따라 같은 Session family를 폐기한다.
- 다른 서비스와 Istio ext_authz는 refresh credential을 검증하거나 저장하지 않는다.

## 실패 책임

| 조건 | 종료 주체 | 외부 상태 | 업무 서비스 호출 |
| --- | --- | --- | --- |
| Bearer JWT 없음 | Istio | `401` | 하지 않음 |
| JWT header, signature 또는 claim 오류 | Istio 또는 Auth adapter | `401` | 하지 않음 |
| Session expired/revoked 또는 사용자 불일치 | Auth Session 상태 검사 | `401` | 하지 않음 |
| Auth/JWKS/Redis/PostgreSQL 상태를 확정할 수 없음 | Istio + Auth | `503` | 하지 않음 |
| 인증 성공 뒤 리소스 소유권 불일치 | 업무 서비스 | `403` | 해당 서비스가 종료 |

모든 실패는 내부 원인, token, 개인정보를 노출하지 않는 공통 오류 응답을 사용한다.

## 서비스 토폴로지 경계

- Auth는 identity와 Session만 소유하며 role/permission을 소유하거나 발급하지 않는다.
- User 서비스는 canonical 사용자 ID와 계정 상태의 후보 서비스다. 운영 승격은 별도 검증 gate를 통과한 뒤 결정한다.
- Backoffice는 별도 retention 경계이며 현재 비활성이다. 이 인증 계약을 이유로 Backoffice Route나 workload를 활성화하거나 User로 대체하지 않는다.
