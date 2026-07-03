# auth-service

인증 계정, 인증 수단, 세션, role 부여를 담당할 Go 서비스 스켈레톤입니다.

현재 범위는 MSA 기본 구조와 운영 엔드포인트 준비입니다. 이메일 가입, 로그인, OAuth/OIDC 연결, refresh token 같은 비즈니스 API는 인증/회원 계약이 확정된 뒤 추가합니다.

## 실행

```bash
go run ./cmd/server
```

기본 주소는 `:8080`입니다. `HTTP_ADDR`로 변경할 수 있습니다.

## 운영 엔드포인트

- `GET /healthz`
- `GET /readyz`
- `GET /metrics`
