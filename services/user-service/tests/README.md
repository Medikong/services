# user-service tests

`user-service` 전용 테스트 영역입니다.

## 구조

```text
services/user-service/
  internal/<package>/*_test.go # 패키지 내부 단위 테스트
  tests/
    integration/               # integration 태그로 실행하는 서비스 통합 테스트
    benchmark/                 # benchmark 태그로 실행하는 서비스 벤치마크
    fixtures/                  # 테스트 fixture와 helper
    testdata/                  # Go 테스트용 정적 데이터
```

내 정보 조회, 사용자 지연 생성, 프로필 수정 같은 비즈니스 API가 확정되면 통합 테스트는 `tests/integration`에 추가합니다.
