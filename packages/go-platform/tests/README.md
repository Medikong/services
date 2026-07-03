# go-platform tests

공용 플랫폼 패키지의 테스트 영역입니다.

## 구조

```text
packages/go-platform/
  <package>/*_test.go       # 패키지 내부 단위 테스트
  tests/
    integration/            # integration 태그로 실행하는 통합 테스트
    benchmark/              # benchmark 태그로 실행하는 벤치마크
    fixtures/               # 테스트 fixture와 helper
    testdata/               # Go 테스트용 정적 데이터
```

단위 테스트는 가능한 한 대상 패키지 옆에 둡니다. 여러 패키지를 조립하거나 외부 계약을 확인하는 테스트만 `tests/integration`에 둡니다.
