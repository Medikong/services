# Auth Service

`auth-service`는 인증 Identity, PasswordCredential, VerificationChallenge, Registration, PasswordReset, AuthenticationIntent, Session, 회전형 refresh credential과 UserAuthState를 소유합니다. `user_id`는 User 서비스가 만든 외부 식별자를 참조하며 프로필, 동의, role, permission, membership은 저장하거나 JWT에 포함하지 않습니다.

## 실행 단위

- `cmd/server`: 공개 HTTP `:8080`, 운영 HTTP `:9090`
- `cmd/worker`: 도메인 Outbox 및 감사 relay, 운영 HTTP `:9092`
- `cmd/migrate`: PostgreSQL core/audit schema와 개발 전용 schema 적용

PostgreSQL이 원장입니다. 도메인별 PostgreSQL repository는 각 `internal/domain/<feature>`에 있고 `internal/app`은 조립과 lifecycle만 담당합니다.

## 보안 계약

- access JWT protected header는 `alg=RS256`, JWKS와 일치하는 `kid`, `typ=JWT`이며 claim은 `iss`, `sub`, `sid`, `aud`, `iat`, `exp`, `jti`만 발급합니다.
- `GET /.well-known/jwks.json`은 active/retiring 공개키, `Cache-Control`, `ETag`를 제공합니다.
- refresh token은 opaque 값이며 DB에는 keyed hash만 저장합니다. 회전된 token 재사용은 같은 family와 Session을 폐기합니다.
- User 계정 상태 proof는 User 서비스의 Ed25519 서명, `aud=auth-service`, `purpose=apply_user_status`, 만료와 path 사용자 일치를 검증합니다.
- 운영 API의 `X-Authorization-Decision`은 별도 인가 경계 port로 검증합니다. 검증 adapter가 주입되지 않으면 기본값은 거부입니다.
- 운영 환경에서 개발 Route나 가상 Email/SMS adapter 설정이 하나라도 활성화되면 시작을 거부합니다.

Auth는 access JWT나 내부 인증 헤더에 role, permission, email, membership 또는 업무 ACL을 넣지 않습니다. 리소스 소유권은 인증된 `X-User-Id`와 도메인 데이터의 owner를 비교해 각 업무 서비스가 판정하며, 불일치는 해당 서비스가 `403`으로 반환합니다.

### 비밀번호 저장 및 전환 정책

- 신규 가입과 비밀번호 재설정은 비밀번호 원문을 복호화 가능한 형태로 저장하지 않습니다. 매번 16바이트 무작위 salt를 만들고 Argon2id `m=32768 KiB`, `t=3`, `p=1`, 32바이트 결과를 `$argon2id$v=19$m=32768,t=3,p=1$<salt>$<hash>` PHC 문자열로 저장합니다. DB의 `hash_algorithm`은 `argon2id`입니다.
- Argon2id는 비밀번호 지문을 만들 때 CPU 계산뿐 아니라 큰 작업 공간인 메모리도 함께 사용합니다. 공격자가 여러 비밀번호를 한꺼번에 추측하려면 추측마다 이 메모리와 계산 시간을 부담해야 합니다.
- PHC parser는 Argon2id/version 19, 정확한 필드 순서, padding 없는 strict base64, 16바이트 salt, 32바이트 hash만 허용합니다. 계산 전 `m=19456~65536 KiB`, `t=1~3`, `p=1~4`를 검사하고 `t=1`이면 `m>=47104 KiB`를 추가로 요구합니다. PHC 전체는 256바이트 이하로 제한해 약한 미승인 조합, 손상값, panic 유발값과 과도한 자원 요청을 거부합니다. 비교는 32바이트끼리 constant-time으로 수행합니다.
- 존재하지 않는 계정과 bcrypt·손상 PHC도 선택한 Argon2id 파라미터의 고정 dummy 검증을 1회 수행한 뒤 같은 일반 인증 실패를 반환합니다. 이 작업은 응답 시간만으로 계정 존재나 legacy credential 여부를 가려내기 어렵게 하지만, 네트워크 전체 시간까지 완전히 같다고 보장하지는 않습니다.
- 신규 비밀번호는 기본 `AUTH_PASSWORD_MIN_LENGTH=12`이며 12~256 범위에서 강화할 수 있습니다. 단위는 Unicode code point이고 최대 256 code point이면서 UTF-8 1024바이트 이하여야 합니다. 공백을 trim하거나 Unicode normalize하지 않으므로 입력한 code point와 UTF-8 바이트열 그대로 해시·비교합니다. OpenAPI의 `minLength: 12`는 모든 배포의 공통 하한이며 강화된 배포에서는 서버의 422 정책 오류가 추가로 적용됩니다.
- 최소 길이 설정은 회원가입과 재설정에 적용합니다. 정책 위반은 모두 `422 AUTH_PASSWORD_POLICY_NOT_MET`와 위반한 길이 조건을 반환합니다. 로그인·재인증은 현재 비밀번호를 정책 오류로 구분하지 않고 계정 존재 여부를 드러내지 않는 `401 AUTH_SIGNIN_FAILED`를 유지합니다.
- 이번 변경은 bcrypt 호환 verifier, 로그인 성공 시 재해시와 데이터 일괄 변환 없이 직접 전환합니다. 기존 bcrypt credential은 로그인·재인증에서 일반 401로 거부되며 사용자가 비밀번호 재설정을 완료해야 Argon2id로 바뀝니다. 배포만으로 기존 Session을 폐기하거나 Identity를 `password_reset_required`로 바꾸지는 않습니다. 재설정 완료 시에는 기존 verifier를 제거하고 기존 Session·refresh family를 폐기하는 revocation fence를 그대로 실행합니다.
- pepper와 GitOps resource 조정은 이 범위에 포함하지 않습니다. 비밀번호, salt, hash 원문은 log, trace, metric label과 공개 오류에 기록하지 않습니다.

### Argon2id 벤치마크 (2026-07-19)

선택 기준은 [OWASP Password Storage Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html)의 Argon2id 최소선 `m=19456,t=2,p=1` 이상과 [RFC 9106](https://www.rfc-editor.org/rfc/rfc9106.html)의 64MiB 두 번째 권고안을 후보에 포함하고, 현재 auth-service 개발 환경에서 실제 PHC 생성·파싱·비교 경로를 측정하는 것이었습니다.

- 환경: Apple M1 Max 10코어(8 performance, 2 efficiency), 32GiB RAM, macOS `darwin/arm64`, Go `1.26.3`, `GOMAXPROCS=10`
- 명령: `GOWORK=off go test -run '^$' -bench 'Argon2|Password' -benchmem -count=5 ./internal/security`
- 반복: 기본 `-benchtime=1s`에서 Go가 후보·operation별 `N`을 자동 선택했고, `-count=5`로 독립 결과 5개를 얻었습니다. 전체 실행 시간은 약 159초였습니다.
- 표 값: 5회 `ns/op`과 `B/op`의 중앙값입니다. `B/op` 괄호에는 5회 최솟값~최댓값을, `ops/s`에는 중앙 `ns/op`에서 계산한 처리량을 적었습니다.

| 후보 | operation | ns/op 중앙값 | B/op 중앙값 (범위) | ops/s |
| --- | --- | ---: | ---: | ---: |
| Argon2id `m=19456,t=2,p=1` | Hash | 24,550,720 | 19,925,374 (19,925,369~19,925,485) | 40.732 |
|  | 정상 Verify | 24,558,250 | 19,925,295 (19,925,291~19,925,316) | 40.720 |
|  | 실패 Verify | 24,507,170 | 19,925,303 (19,925,299~19,925,417) | 40.804 |
|  | 병렬 Verify | 3,456,050 | 19,925,296 (19,925,286~19,925,314) | 289.347 |
| Argon2id `m=32768,t=3,p=1` | Hash | 63,202,510 | 33,557,102 (33,557,096~33,557,108) | 15.822 |
|  | 정상 Verify | 62,601,100 | 33,557,024 (33,557,024~33,557,096) | 15.974 |
|  | 실패 Verify | 62,966,230 | 33,557,024 (33,557,024~33,557,050) | 15.882 |
|  | 병렬 Verify | 8,949,900 | 33,557,068 (33,557,035~33,557,073) | 111.733 |
| Argon2id `m=47104,t=1,p=1` | Hash | 30,628,540 | 48,236,651 (48,236,648~48,236,665) | 32.649 |
|  | 정상 Verify | 30,648,560 | 48,236,591 (48,236,576~48,236,646) | 32.628 |
|  | 실패 Verify | 30,689,370 | 48,236,607 (48,236,576~48,236,636) | 32.585 |
|  | 병렬 Verify | 4,337,910 | 48,236,596 (48,236,579~48,236,599) | 230.526 |
| Argon2id `m=65536,t=3,p=1` | Hash | 131,934,860 | 67,111,528 (67,111,528~67,111,588) | 7.579 |
|  | 정상 Verify | 132,334,310 | 67,111,470 (67,111,456~67,111,516) | 7.557 |
|  | 실패 Verify | 133,417,010 | 67,111,456 (67,111,456~67,111,530) | 7.495 |
|  | 병렬 Verify | 20,308,690 | 67,111,540 (67,111,471~67,111,569) | 49.240 |
| Argon2id `m=65536,t=3,p=4` | Hash | 35,318,430 | 67,115,631 (67,115,617~67,115,631) | 28.314 |
|  | 정상 Verify | 35,945,520 | 67,115,552 (67,115,528~67,115,568) | 27.820 |
|  | 실패 Verify | 35,556,740 | 67,115,553 (67,115,535~67,116,112) | 28.124 |
|  | 병렬 Verify | 18,902,500 | 67,115,503 (67,115,501~67,115,509) | 52.903 |
| bcrypt cost 10 비교선 | Hash | 67,324,780 | 5,308 (5,297~5,320) | 14.853 |
|  | 정상 Verify | 67,185,860 | 5,324 (5,315~5,338) | 14.884 |
|  | 실패 Verify | 67,109,500 | 5,338 (5,326~5,338) | 14.901 |
|  | 병렬 Verify | 7,795,330 | 5,253 (5,251~5,260) | 128.282 |

`m=32768,t=3,p=1`을 선택했습니다. 단일 Verify 지연이 bcrypt cost 10과 비슷한 약 62ms이고, 10코어 병렬 측정에서도 약 112 ops/s를 유지하면서 동시 10건의 작업 메모리를 대략 320MiB로 제한합니다. 64MiB 후보는 단일 호출에서 강하지만 동시 10건이면 약 640MiB가 필요하고 병렬 처리량이 약 52~55 ops/s로 낮았습니다. `m=19456,t=2,p=1`은 메모리와 지연이 가장 낮지만 이 장비에서 약 25ms라 추가 방어 여력이 있었고, `m=47104,t=1,p=1`은 처리량은 높지만 동시 메모리 비용이 더 큽니다.

이 결과는 개발 장비의 microbenchmark입니다. `B/op`은 누적 할당량이지 peak RSS가 아니며, 병렬 `ns/op`은 개별 요청의 p50 지연이 아니라 병렬 완료 작업당 평균입니다. 실제 container limit, 로그인 burst, rate limit와 공유 workload는 검증하지 않았으므로 배포 전 production-like 자원 제한에서 부하 검증해야 합니다. 파라미터를 바꿀 때는 같은 벤치마크와 기존 PHC 허용 범위를 함께 재검토합니다.

### 현재 코드와 배포 blocker

현재 저장소 코드의 access JWT signing과 JWKS는 RS256입니다. 다만 issuer와 active deployment에는 다음 차이가 남아 있습니다.

- 현재 config는 `AUTH_JWT_ISSUER`가 없으면 `ServiceName`(`auth-service`)으로 fallback하고 operational validation도 fallback 사용을 거부하지 않습니다. 운영에서 issuer를 명시적으로 요구하고 기본값을 두지 않는 것은 승인된 목표 정책이며 아직 구현 사실이 아닙니다.
- 공통 `values/services/auth.yaml`은 legacy `AUTH_JWT_SECRET`을 선언하지만 Helm의 목록 coalescing에서 private-dev/aws-dev overlay의 전체 `container.env` 목록으로 대체됩니다. 따라서 두 active effective stack에는 각각 legacy `JWT_SECRET` 하나만 남습니다. 저장소의 dev overlay도 `JWT_SECRET`을 선언하지만 두 active Application에서 참조하지 않습니다.
- 활성 Kong shared resources는 HS256 JWT credential을 render합니다.
- 활성 private-dev/aws-dev values stack은 현재 코드가 요구하는 RS256 private key와 key ID를 공급하지 않습니다.
- 활성 Kong `ticketing-identity-headers`는 JWT의 email/role claim을 읽어 `X-User-Email`/`X-User-Role`을 만들고, `ticketing-role-*`는 role claim으로 인가와 legacy `403`을 결정합니다. Notification, Interest, Order, Payment ingress attachment가 남아 있습니다.
- Notification과 Interest runtime은 아직 `X-User-Role`을 신뢰해 role 기반 접근을 판정합니다.

따라서 현재 GitOps desired state는 최신 Auth 코드와 호환되는 RS256-ready 또는 identity-only-ready 배포가 아닙니다. 후속 Auth config 작업은 운영 issuer fallback을 거부해야 하고, 후속 GitOps 작업은 legacy JWT secret/Kong credential과 role/email plugin 경로를 제거한 뒤 private key, key ID, issuer를 승인된 Secret-backed 경로로 공급해야 합니다. Notification/Interest 후속 작업도 role header trust를 제거해야 합니다. 이 README 변경은 배포 manifest나 서비스 runtime을 수정하거나 blocker를 해소하지 않습니다.

### Istio HTTP ext_authz 계약 및 배포 상태

> 구현 상태: 현재 stage worktree의 runtime 소스는 `/internal/ext-authz`와 모든 하위 path의 Handler를 router에 등록합니다. 이 경로는 공개 OpenAPI에 노출하지 않습니다. 활성 GitOps는 아직 legacy JWT/Kong 구성이고 해당 Handler를 배포하거나 외부 route/Istio ext_authz에 연결하지 않았습니다. RS256 key/issuer 공급과 Istio 연결은 후속 GitOps 작업입니다.

Istio는 외부 요청의 `X-User-*`, `X-Session-*`, `X-Token-*`을 먼저 제거하고 JWT를 검증한 뒤 Auth의 `/internal/ext-authz`에 HTTP check를 보냅니다. request body는 전달하지 않고 `Authorization`, `X-Request-Id`, 원래 method/path만 전달합니다.

| 결과 | HTTP | Auth adapter 출력 | 책임 |
| --- | --- | --- | --- |
| identity 확인 | `200` | `X-User-Id`, `X-Session-Id`, `X-Token-Id` | Auth Session 상태 검사 |
| JWT/Session 인증 거부 | `401` | 없음 | Istio/Auth |
| Auth/JWKS/Redis/PostgreSQL 상태 미확정 | `503` | 없음 | Istio/Auth, fail closed |
| 인증 뒤 resource ownership 불일치 | `403` | 해당 없음 | 업무 서비스 |

세 성공 헤더 외에 role, permission, email 등은 만들지 않습니다. `/internal/ext-authz`는 내부 전용 runtime 경로이며 외부 Gateway Route와 공개 OpenAPI에 등록하지 않습니다. 지정된 Ingress workload identity만 호출하도록 제한하는 것은 후속 GitOps/Istio 정책의 책임입니다. Handler의 초기 timeout은 200ms이며 자동 재시도와 fail-open이 없습니다.

Route는 명시적 allowlist로 분류합니다. 공개 가입/로그인/비밀번호 재설정 Route는 Auth flow credential을, 보호 Route와 `GET /api/v1/auth/context`는 JWT와 Session을, refresh/logout Route는 refresh credential을 검증합니다. JWKS Route는 사용자 credential 대신 mesh network policy로 제한합니다.

### 서비스 토폴로지

- User 서비스는 canonical 사용자 ID와 계정 상태의 후보 서비스이며, 별도 검증 gate를 통과하기 전에는 운영 canonical로 승격하지 않습니다.
- Backoffice는 Auth/User와 분리된 retention 경계이고 현재 비활성입니다. 이 계약은 Backoffice Route나 workload를 활성화하거나 Backoffice를 User로 대체하지 않습니다.

가입 완료는 비동기 이벤트를 기다리지 않습니다. API.A.300-05가 Auth 서명의 `registrationCompletionProof`를 발급하고, User 서비스가 사용자를 만든 뒤 API.A.300-06에 User 서명의 `userCreationProof`를 제출하면 Auth가 IdentityLink, UserAuthState, Session을 한 트랜잭션에서 확정합니다.

도메인 Outbox relay는 `NewWorkerWithPublisher`에 배포 환경의 신뢰할 수 있는 publisher adapter를 주입했을 때만 실행됩니다. 저장소에는 아직 특정 broker의 주소·topic·credential 기준이 없으므로 기본 실행 파일은 이를 추측하지 않고 감사 relay만 시작합니다.

RSA 개인키 예시는 다음처럼 만들 수 있습니다. 실제 환경에서는 파일을 저장소에 넣지 않고 secret manager가 `AUTH_JWT_PRIVATE_KEY_PEM`으로 주입해야 합니다.

```bash
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:3072
```

## 실행

```bash
cp .env.example .env
# .env의 필수 key와 DATABASE_URL을 채운 뒤
set -a
. ./.env
set +a
GOWORK=off go run ./cmd/migrate
GOWORK=off go run ./cmd/server
```

## OpenAPI 원장

OpenAPI 원장은 sibling `archive/blueprint/50-service-design/A_300_auth/A_300_40-api/openapi/` 하나입니다. 서비스에는 배포와 CI용 bundle 및 원장 hash만 둡니다.

```bash
task auth-openapi-sync
task auth-openapi-check
```

운영 bundle에는 `/api/v1/dev/` Route가 들어가면 검사가 실패합니다. `archive` checkout이 없는 CI에서는 `scripts/check-openapi.sh --snapshot-only`로 bundle 자체를 검증합니다.

## 검증

```bash
GOWORK=off go test ./...
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
GOWORK=off go test -tags=integration -count=1 ./tests/integration
docker build -f services/auth-service/Dockerfile -t auth-service:dev .
```

이미지 기본 entrypoint는 `/app/server`입니다. migration Job은 `/app/migrate`, worker 배포는 `/app/worker`를 실행합니다.
