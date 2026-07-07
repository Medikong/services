# AGENTS.md

## 프로그래밍 규칙

- Go: `docs/programming/go/RULE.md`
- Python: `docs/programming/python/RULE.md`

이 repo는 서비스 코드, 테스트, OpenAPI 계약, Docker image build, registry push workflow를 담당한다. 배포 선언, Argo CD, Terraform, Ansible, 클러스터 운영 파일은 sibling `gitops` 또는 `infra` repo에서 다룬다.

서비스를 추가하거나 수정할 때는 공통 서비스 계약을 함께 유지한다: `/healthz`, `/readyz`, `/metrics`, OpenAPI 문서, Dockerfile, unit test, integration test, structured JSON log.

## 기술 스택 기준

새 서비스나 공통 패키지를 만들 때는 아래 기술 스택을 우선 후보로 검토한다. 표준 라이브러리와 repo-local 공통 패키지를 먼저 사용하고, 외부 라이브러리는 목적과 운영 영향을 설명할 수 있을 때 도입한다.

### Python

| 영역 | 기본 후보 |
| --- | --- |
| Backend | Python, FastAPI, Starlette, Pydantic v2, JWT |
| ASGI Runtime | Uvicorn, Gunicorn |
| Auth & Security | PyJWT, python-jose, passlib, pwdlib, bcrypt, argon2-cffi |
| Data & Messaging | PostgreSQL, MongoDB, Kafka, SQLAlchemy, Alembic, asyncpg, psycopg, PyMongo |
| Cache & Queue | redis-py, Celery, Dramatiq, arq |
| Messaging Client | aiokafka, confluent-kafka |
| Config | pydantic-settings, python-dotenv |
| Platform | Docker, Kubernetes, Istio |
| CI/CD & IaC | GitHub Actions, Helm, Argo CD, Terraform, AWS, Amazon ECR |
| Logging | structlog, standard logging |
| Observability | OpenTelemetry, prometheus-client, Prometheus, Alertmanager, Grafana, Loki, Tempo |
| Quality & Test | pytest, pytest-asyncio, httpx, respx, testcontainers-python, factory-boy, k6, Postman, Newman, Trivy |
| Static Analysis | ruff, mypy, pyright, bandit, pip-audit |
| Docs & API | OpenAPI, Swagger UI, Redoc |

### Go

| 영역 | 기본 후보 |
| --- | --- |
| Backend | Go, standard library, `context`, `errors`, `net/http`, `log/slog` |
| HTTP & API | `net/http`, oapi-codegen |
| Data & Messaging | PostgreSQL, MongoDB, Kafka, Redis, pgx, database/sql, sqlc, mongo-go-driver, go-redis |
| Migration | goose, golang-migrate |
| Messaging Client | segmentio/kafka-go, confluent-kafka-go, Watermill |
| Async Jobs | Asynq, Watermill, Kafka consumer worker |
| Auth & Security | golang-jwt/jwt, bcrypt, argon2, JOSE/JWK 라이브러리 |
| Config | envconfig, cleanenv, viper |
| Platform | Docker, Kubernetes, Istio |
| CI/CD & IaC | GitHub Actions, Helm, Argo CD, Terraform, AWS, Amazon ECR |
| Logging | `log/slog`, `packages/go-platform/logger` |
| Observability | OpenTelemetry Go, Prometheus client_golang, Prometheus, Alertmanager, Grafana, Loki, Tempo |
| Error Handling | github.com/samber/oops, errors.Is, errors.As |
| Resilience & Concurrency | cenkalti/backoff, sony/gobreaker, x/sync/errgroup, singleflight |
| Validation | go-playground/validator, ozzo-validation |
| Quality & Test | testing, httptest, testify, testcontainers-go, gomock, mockery, k6, Postman, Newman, Trivy |
| Static Analysis | gofmt, go vet, staticcheck, golangci-lint, govulncheck |
| CLI & Task | cobra, Taskfile |
| Docs & API | OpenAPI, Swagger, Swagger UI, oapi-codegen |
| Reference Repositories | zeromicro/go-zero |
