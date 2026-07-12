# Auth HTTP E2E 계약 테스트 매트릭스

이 문서는 auth-service HTTP E2E 테스트의 구현 범위를 기록하는 보조 자료다. HTTP 계약 원장은 sibling `archive` 저장소의 아래 문서다.

- 운영 원장: `archive/blueprint/50-service-design/A_300_auth/A_300_40-api/openapi/openapi.yaml`
- 개발 전용 원장: `archive/blueprint/50-service-design/A_300_auth/A_300_40-api/openapi/dev.openapi.yaml`
- 서비스 bundle: 원장에서 동기화한 snapshot이며 `task auth-openapi-check`의 hash/sync 규칙을 따른다.

이 문서와 테스트 fixture에 별도 API 정의를 만들지 않는다. 상태 코드, schema, header, cookie 또는 오류 코드가 달라지면 archive 원장을 먼저 검토하고 정해진 절차로 service bundle을 갱신한다.

## 공통 기준

- 새 E2E는 Testcontainers PostgreSQL, embedded migration, 실제 auth server와 HTTP client를 사용한다.
- `route`는 기존 router 계약 테스트가 method/path 등록만 확인한다는 뜻이다.
- `common`은 기존 helper 테스트가 envelope, `ProblemDetails`, request ID, strict JSON, cookie/bearer, CSRF/Origin, no-store를 단위 수준에서 확인한다는 뜻이다.
- 새 E2E는 controller나 application service를 직접 호출하지 않고 실제 route와 middleware를 통과해야 한다.
- 운영 server에는 API.A.300-01~29만 등록한다. API.A.300-30은 `SERVICE_ENVIRONMENT=test`와 개발 route, Virtual Adapter, access token, encryption key가 모두 유효한 별도 server에서만 확인한다.
- 성공 JSON은 `data/meta`, 오류는 `application/problem+json`과 `ProblemDetails`, 모든 응답은 `X-Request-Id`와 `Cache-Control: no-store`를 확인한다. 204 응답은 body가 없어야 한다.
- 실패 메시지와 진단 출력에 이메일, 휴대폰, 비밀번호, OTP, cookie, token을 남기지 않는다.

## API 매트릭스

| API | 환경 | Method / path | 성공 | 대표 오류 | 기존 보장 | 새 실제 HTTP E2E가 확인할 항목 |
| --- | --- | --- | --- | --- | --- | --- |
| API.A.300-01 | 운영 | `POST /api/v1/auth/intents` | 201 | 400 `AUTH_REDIRECT_INVALID` | route + common | strict body/header, web auth cookie와 mobile auth-flow token |
| API.A.300-02 | 운영 | `GET /api/v1/auth/methods` | 200 | 404 `AUTH_INTENT_NOT_FOUND` | route + common | intent query와 소유 credential, methods envelope |
| API.A.300-03 | 운영 | `POST /api/v1/auth/registrations` | 201 | 409 `AUTH_IDENTIFIER_UNAVAILABLE` | route + common | 가입 입력, CSRF/Origin, registration/status credential |
| API.A.300-04 | 운영 | `POST /api/v1/auth/registrations/{registrationId}/challenges` | 201 | 400 `AUTH_INPUT_INVALID` | route + common | challenge 발급과 암호화된 delivery payload |
| API.A.300-05 | 운영 | `POST /api/v1/auth/registrations/{registrationId}/challenges/{challengeId}/verify` | 200 | 400 `AUTH_CHALLENGE_FAILED` | route + common | 실제 OTP 검증, Challenge 단일 소비, 동일·동시 재시도 |
| API.A.300-06 | 운영 | `POST /api/v1/auth/registrations/{registrationId}/complete` | 200 / 202 | 409 `AUTH_VERIFICATION_REQUIRED` | route + common | User link inbox 전후 202→200, session/cookie 또는 token 발급, idempotency |
| API.A.300-07 | 운영 | `POST /api/v1/auth/signins/email` | 200 | 401 `AUTH_SIGNIN_FAILED` | route + common + signin DTO | 이메일 로그인, web/mobile credential delivery, 잘못된 암호 |
| API.A.300-08 | 운영 | `POST /api/v1/auth/signins/phone/challenges` | 202 | 400 `AUTH_INPUT_INVALID` | route + common | 휴대폰 challenge 발급, strict phone input, 소유 credential |
| API.A.300-09 | 운영 | `POST /api/v1/auth/signins/phone/challenges/{challengeId}/verify` | 200 | 400 `AUTH_CHALLENGE_FAILED` | route + common + signin DTO | 실제 OTP 휴대폰 로그인과 mobile session 발급 |
| API.A.300-10 | 운영 | `POST /api/v1/auth/password-resets` | 202 | 400 `AUTH_INPUT_INVALID` | route + common | email reset 시작과 strict identifier input |
| API.A.300-11 | 운영 | `POST /api/v1/auth/password-resets/{passwordResetId}/challenges` | 202 | 400 `AUTH_INPUT_INVALID` | route + common | reset challenge 발급과 strict method input |
| API.A.300-12 | 운영 | `POST /api/v1/auth/password-resets/{passwordResetId}/challenges/{challengeId}/verify` | 200 | 400 `AUTH_CHALLENGE_FAILED` | route + common | 실제 OTP 검증과 mobile reset grant |
| API.A.300-13 | 운영 | `PUT /api/v1/auth/password-resets/{passwordResetId}/password` | 204 | 422 `AUTH_PASSWORD_POLICY_NOT_MET` | route + common | password 정책 오류, 변경 완료, 이전/새 암호 결과 |
| API.A.300-14 | 운영 | `POST /api/v1/auth/sessions/refresh` | 200 | 401 `AUTH_SESSION_REVOKED` | route + common | refresh rotation, 동일 요청 retry, reuse 감지와 session family 폐기 |
| API.A.300-15 | 운영 | `POST /api/v1/auth/sessions/logout` | 204 | 401 `AUTH_SESSION_REQUIRED` | route + common | web cookie/mobile refresh credential, CSRF, 반복 logout과 cookie 정리 |
| API.A.300-16 | 운영 | `GET /api/v1/auth/context` | 200 | 400 `AUTH_MULTIPLE_CREDENTIALS` | route + common | anonymous/web/mobile context, cookie+bearer 동시 제출 거부, `Vary` |
| API.A.300-17 | 운영 | `POST /api/v1/auth/reauthentications/email` | 200 | 401 `AUTH_SIGNIN_FAILED` | route + common | 목적 한정 proof, session rotation, 잘못된 암호와 CSRF |
| API.A.300-18 | 운영 | `POST /api/v1/auth/method-links` | 200 / 201 | 410 `AUTH_REAUTHENTICATION_PROOF_INVALID` | route + common | 이미 연결됨/새 link intent, proof binding, idempotency |
| API.A.300-19 | 운영 | `POST /api/v1/auth/method-links/{linkIntentId}/challenges` | 201 | 404 `AUTH_IDENTITY_LINK_NOT_FOUND` | route + common | method-link challenge 발급과 session 소유권 |
| API.A.300-20 | 운영 | `POST /api/v1/auth/method-links/{linkIntentId}/complete` | 200 | 400 `AUTH_CHALLENGE_FAILED` | route + common | 실제 OTP 검증과 identity link 완료 |
| API.A.300-21 | 운영 | `POST /api/v1/auth/phone-replacements` | 201 | 410 `AUTH_REAUTHENTICATION_PROOF_INVALID` | route + common | 새 휴대폰과 proof binding, identity 충돌, idempotency |
| API.A.300-22 | 운영 | `POST /api/v1/auth/phone-replacements/{replacementId}/challenges` | 201 | 404 `AUTH_IDENTITY_LINK_NOT_FOUND` | route + common | replacement challenge 발급과 소유권 |
| API.A.300-23 | 운영 | `POST /api/v1/auth/phone-replacements/{replacementId}/complete` | 200 | 410 `AUTH_SESSION_DELIVERY_EXPIRED` | route + common | replacement 완료, session 전달 실패 뒤 동일 요청 복구, credential rotation |
| API.A.300-24 | 운영 | `GET /api/v1/operator/auth/users/{userId}` | 200 | 403 `AUTH_FORBIDDEN` | route + common | operator role, audit reason header, strict masked response와 민감 값 미노출 |
| API.A.300-25 | 운영 | `GET /api/v1/operator/auth/policies` | 200 | 403 `AUTH_FORBIDDEN` | route + common | active policy snapshot, strong `ETag`, operator role |
| API.A.300-26 | 운영 | `PATCH /api/v1/operator/auth/policies/{policyName}` | 200 | 412 `AUTH_POLICY_PRECONDITION_FAILED` | route + common | `If-Match`, 새 snapshot/ETag, CSRF와 동일 key replay |
| API.A.300-27 | 운영 | `POST /api/v1/operator/auth/manual-actions` | 200 | 409 `AUTH_APPROVAL_REQUIRED` | route + common | approval/target version, idempotency, audit-safe response |
| API.A.300-28 | 운영 | `GET /api/v1/auth/registrations/{registrationId}` | 200 | 404 `AUTH_REGISTRATION_NOT_FOUND` | route + common | auth-flow/status token별 소유권, 202 가입의 상태 전이 |
| API.A.300-29 | 운영 | `POST /api/v1/auth/intents/{intentId}/action-resume` | 200 | 410 `AUTH_INTENT_EXPIRED` | route + common | 인증된 session, action payload와 동일 key replay |
| API.A.300-30 | 개발 | `GET /api/v1/dev/auth/verification-messages/{challengeId}` | 200 | 404 `AUTH_VIRTUAL_MESSAGE_NOT_FOUND` | dev route + common + dev token helper | 운영 404, 개발 ready, access token+소유 credential, terminal 410 |

## 시나리오 묶음

- 가입: 01 → 02 → 03 → 04 → 30 → 05 → 06 → User link inbox → 06 → 28
- 로그인: 01 → 07, 01 → 08 → 30 → 09
- 비밀번호 재설정: 01 → 10 → 11 → 30 → 12 → 13
- Session: 14 → 16 → 15, refresh retry/reuse 동시 요청
- 인증 수단: 17 → 18 → 19 → 30 → 20
- 휴대폰 교체: 17 → 21 → 22 → 30 → 23, credential 전달 실패 뒤 재시도
- 운영자/행동 재개: 24 → 25 → 26 → 27, 별도 인증 완료 뒤 29

각 묶음은 HTTP 응답으로 다음 단계의 opaque ID와 credential을 얻는다. DB 직접 조작은 외부 inbox, 정책 seed, 전달 실패 주입처럼 공개 HTTP API로 만들 수 없는 fixture 준비와 결과 확인에만 제한한다.
