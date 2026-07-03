# go-authz tests

공통 인가 모델 테스트 영역입니다.

단위 테스트는 `acl`, `principal`, `rbac` 같은 대상 패키지 옆에 둡니다. 여러 인가 모델을 조합하는 검증은 `tests/integration`, 정책 평가 성능 확인은 `tests/benchmark`에 둡니다.
