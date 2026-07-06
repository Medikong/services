# user-service

`user_id` 기반 사용자, 최소 프로필, 사용자 상태를 담당하는 Go 서비스입니다.

auth-service가 발급하거나 전달하는 `user_id`를 기준으로 사용자 레코드를 지연 생성하고, 실명, 닉네임, 프로필 아이콘 같은 사용자 프로필 정보를 다룹니다. auth-service는 이 프로필 정보를 소유하지 않습니다.

## 구조

- `internal/app`: repository 선택과 HTTP route wiring
- `internal/domain/user`: 사용자 모델, use case, repository port, memory/PostgreSQL repository
- `internal/platform/config`: 환경 설정 로딩
- `internal/transport/http`: public/internal user route와 operational route

## 실행

```bash
go run ./cmd/server
```

기본 주소는 `:8080`입니다. `HTTP_ADDR`로 변경할 수 있습니다.

`DATABASE_URL`이 있으면 PostgreSQL repository를 사용하고, 없으면 local/dev용 memory repository를 사용합니다.

## 사용자 API

- `POST /v1/internal/users/ensure`
- `GET /v1/users/me`
- `PATCH /v1/users/me/profile`
- `GET /v1/users/{userId}`

## 운영 엔드포인트

- `GET /healthz`
- `GET /readyz`
- `GET /metrics`
