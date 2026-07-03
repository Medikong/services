# auth-service tests

`auth-service` 전용 테스트 영역입니다.

## 구조

```text
services/auth-service/
  internal/<package>/*_test.go # 패키지 내부 단위 테스트
  tests/
    integration/               # integration 태그로 실행하는 서비스 통합 테스트
    benchmark/                 # benchmark 태그로 실행하는 서비스 벤치마크
    fixtures/                  # 테스트 fixture와 helper
    testdata/                  # Go 테스트용 정적 데이터
```

이메일 가입, 로그인, OAuth/OIDC, 세션 같은 비즈니스 API가 확정되면 통합 테스트는 `tests/integration`에 추가합니다.
