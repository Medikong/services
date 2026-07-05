# 테스트 실행 가이드

이 프로젝트의 테스트 진입점은 루트 `Taskfile.yml`이다. 개발자 로컬에는 Docker, Docker Compose, Task를 준비하고, Go test와 Python smoke/benchmark 스크립트 실행은 Task로 반복한다.

업무 흐름을 사람이 직접 검증하거나 장애를 주입해 확인하는 절차는 배포/인프라 repo에서 별도 문서로 관리한다.

## 테스트 범위

| 구분 | 도구 | 대상 |
| --- | --- | --- |
| Go 단위 테스트 | Go test | `packages/go-*`, `services/auth-service`, `services/user-service`, `services/coupon-service`, `services/backoffice-service` |
| Go 통합 테스트 | Go test + `integration` build tag | Go 서비스와 공용 패키지의 조립 경계 |
| Go 벤치마크 | Go test + `benchmark` build tag | Go handler, 공용 패키지, 핵심 경로 성능 |
| Service E2E | Docker Compose, PostgreSQL, Python 시나리오 스크립트 | auth/user/coupon/backoffice DNS를 직접 호출해 DropMong 기능 시나리오 검증 |
| Observability E2E | Docker Compose, OpenTelemetry Collector, Tempo, Grafana, Python smoke 컨테이너 | Go inbound request span이 OTLP -> Collector -> Tempo 경로로 적재되는지 검증 |
| Gateway E2E | 별도 future/Kubernetes scope | Kong/JWT/Ingress 라우팅, MetalLB 노출, gateway trace boundary 검증 |

## 폴더 구조

```text
tests/
  docker/
    Dockerfile
  e2e/
    docker-compose.yml
    observability/
      docker-compose.yml
      otel-collector.yml
      tempo.yml
      grafana/
        provisioning/
          datasources/
            tempo.yml
      scripts/
        trace-smoke.py
    scripts/
      dropmong_scenarios.py
    postgres-init/
      01-create-databases.sql
  scripts/
    dropmong_api_benchmark.py
```

테스트 실행 Task 본문은 `tests/Taskfile.yml`에 두고, 루트 `Taskfile.yml`은 같은 명령 이름으로 위임한다.

```text
services/auth-service/tests/
services/user-service/tests/
services/coupon-service/tests/
services/backoffice-service/tests/
```

Go 서비스와 공용 Go 패키지는 다음 구조를 사용한다.

```text
services/<go-service>/
  internal/<package>/*_test.go # 패키지 내부 단위 테스트
  tests/
    integration/               # integration 태그로 실행하는 통합 테스트
    benchmark/                 # benchmark 태그로 실행하는 벤치마크
    fixtures/                  # fixture와 test helper
    testdata/                  # Go 테스트용 정적 데이터

packages/go-<package>/
  <package>/*_test.go
  tests/
    integration/
    benchmark/
    fixtures/
    testdata/
```

## 로컬 Go 테스트

Go 단위 테스트는 루트에서 실행한다.

```bash
task test-go-unit
```

통합 테스트는 `integration` build tag로 분리한다. 데이터베이스나 외부 의존성이 붙는 테스트는 이 범위에 둔다.

```bash
task test-go-integration
```

벤치마크는 `benchmark` build tag로 분리한다. 실행 시간은 `GO_BENCH_TIME`으로 조정할 수 있다.

```bash
task test-go-benchmark
task test-go-benchmark GO_BENCH_TIME=3s
```

## Service E2E 테스트 흐름

`dropmong_scenarios.py`는 Docker Compose 네트워크 DNS로 각 서비스를 직접 호출해 다음 DropMong baseline 흐름을 검증한다. Kong/JWT/Ingress는 기본 `task test-e2e` 범위가 아니며, auth-service가 발급한 `X-Principal` 헤더를 서비스 요청에 직접 넣는다.

1. `auth-service`에서 고객/운영자 테스트 토큰을 발급하고 인증 실패 응답을 확인한다.
2. `user-service`에서 회원 프로필을 지연 생성하고 사용자 조회 권한을 확인한다.
3. 준비 전 쿠폰 발급이 명확한 `coupon.policy_not_found` 오류로 실패하는지 확인한다.
4. `backoffice-service`에서 운영자가 상품, 재고, 판매 시간, 쿠폰 정책을 준비한다.
5. `coupon-service`에서 발급 성공, 중복 요청, 재고 소진, 동시 요청 결과를 확인한다.

## 로컬 Service E2E 실행

`task test-e2e`는 Docker Compose로 PostgreSQL과 Go 서비스를 띄운 뒤 같은 Compose 네트워크에서 Python 시나리오 스크립트를 실행한다. 서비스 URL은 Compose DNS 이름을 사용한다.

```bash
task test-e2e
```

기본 URL은 다음과 같다.

| 서비스 | 기본 URL |
| --- | --- |
| `auth-service` | `http://auth-service:8080` |
| `user-service` | `http://user-service:8080` |
| `coupon-service` | `http://coupon-service:8080` |
| `backoffice-service` | `http://backoffice-service:8080` |

`task test-e2e`는 Docker Compose `--wait`로 서비스 healthcheck가 `healthy`가 될 때까지 기다린 뒤 `tests/e2e/scripts/dropmong_scenarios.py`를 실행한다.

## 로컬 Observability E2E 실행

`task tests:test-observability-e2e`는 기능 시나리오 검증과 별개로 OpenTelemetry trace 수집 경로만 확인한다. Docker Compose로 `coupon-service`, PostgreSQL, OpenTelemetry Collector, Tempo, Grafana를 띄운 뒤 Python smoke 컨테이너가 실제 API 요청을 보내고 Tempo API를 polling한다.

```bash
task tests:test-observability-e2e
```

이 테스트가 확인하는 경로는 다음과 같다.

```text
Go OpenTelemetry instrumentation
-> OTLP
-> OpenTelemetry Collector OTLP receiver
-> Tempo
```

`/healthz`, `/readyz`는 서비스 readiness 대기용으로만 사용한다. `/healthz`, `/readyz`, `/metrics`는 Go OpenTelemetry middleware의 trace 제외 기본값이라 probe와 scrape가 Tempo trace 잡음으로 쌓이지 않는다. smoke는 세 endpoint에 고유 `X-Request-Id`를 붙여 호출한 뒤 Tempo에서 해당 trace가 없어야 한다고 확인한다. trace 생성 요청은 기본값으로 coupon-service의 `GET /internal/coupon-policies/obs-policy`를 호출한다. Grafana는 `Tempo` datasource provisioning과 수동 조회 보조 용도다. 자동 성공/실패 판정은 Grafana UI가 아니라 Tempo `/api/search`와 trace 상세 조회로 수행한다. Prometheus target/metric 확인과 Loki 로그 수집은 GitOps 관측성 stack 검증에서 별도로 다룬다.

## CI

`.github/workflows/service-tests.yml`은 PR 변경 경로를 기준으로 테스트 대상만 만든다. 테스트 job은 `task test-services SERVICES="<services>"`를 한 번 실행하고, Docker image 빌드 검증은 별도 workflow인 `.github/workflows/image-build.yml`이 독립적으로 담당한다. 두 workflow는 분리되어 있어 이미지 빌드 화면에서 단위 테스트 결과를 함께 보지 않는다.

`services/<service>/**` 변경은 해당 서비스 테스트와 해당 이미지를 선택한다. `tests/**` 변경은 Service Tests workflow의 전체 서비스 테스트만 실행하며, `packages/**`와 `Taskfile.yml` 변경은 테스트와 이미지 빌드 양쪽에서 전체 대상을 선택한다. `.github/workflows/image-build.yml` 또는 `.github/workflows/image-publish.yml` 변경은 image build workflow에서 전체 이미지 빌드를 실행한다. `contracts/**`나 문서만 변경된 PR은 서비스 테스트와 이미지 빌드 모두 no-op 성공 job으로 끝난다. `main` push의 registry publish는 `.github/workflows/image-publish.yml`이 담당한다.

`.github/workflows/image-publish.yml`은 `main` push 또는 수동 실행에서 GitHub Actions runner 안의 `registry:2`를 현재 publish registry인 `localhost:5000`으로 띄운다. 각 image는 commit SHA tag로 `task app-image-build SERVICE=<image> IMAGE_REGISTRY=localhost:5000 IMAGE_TAG=<commit-sha>`를 실행한 뒤 push하고, registry digest를 수집해 `image-publish-deploy-plan` artifact와 workflow summary에 남긴다. 나중에 영속 registry를 연결할 때는 registry URL과 인증 단계만 교체한다. 이 산출물은 후속 `gitops` repo image tag/digest 업데이트의 입력 계획이며, 현재 단계에서는 Kubernetes 배포 선언 수정이나 Argo CD sync를 수행하지 않는다.

CI는 단위 테스트 성공/실패와 관계없이 `unit-test-reports` artifact를 업로드한다. artifact 안의 `tests/tmp/reports/unit/<service>/summary.json`에는 서비스명, 성공/실패 상태, exit code, 시작/종료 시각, 실행 시간과 테스트 메트릭이 기록된다. GitHub Actions summary에는 `tests/tmp/reports/unit/summary.md`의 전체 단위 테스트 표가 표시된다.

`.github/workflows/e2e.yml`은 `main` push, 수동 실행, 매일 03:00 KST 정기 실행에서 `task test-e2e`를 실행한다. GitHub runner 안에서 Docker Compose 기반 PostgreSQL E2E stack과 DropMong 시나리오 검증 스크립트를 함께 실행한다.

Kong/JWT/Ingress 검증은 기본 E2E와 분리한다. 이후 필요해지면 `task test-gateway-e2e` 같은 별도 타깃에서 MetalLB IP 또는 Ingress 주소, JWT 생성, Gateway 라우팅 검증을 다룬다.

## 실패 시 점검 포인트

| 증상 | 점검 |
| --- | --- |
| Docker build 실패 | Docker Desktop/Engine 실행 상태 확인 |
| `docker compose` 실패 | Docker Compose plugin 설치 여부 확인 |
| pytest import 실패 | `task test-unit`로 Docker 테스트 러너를 통해 실행했는지 확인 |
| DB 연결 실패 | `DATABASE_URL` 값과 PostgreSQL 실행 상태 확인 |
| Observability smoke 실패 | `docker compose -p dropmong-observability-e2e -f tests/e2e/observability/docker-compose.yml logs otel-collector tempo coupon-service` 확인 |
| 시나리오 401 | Gateway E2E가 아닌지, 서비스가 요구하는 인증 헤더가 누락됐는지 확인 |
| 시나리오 403 | 운영자/고객 role과 요청 path 권한 관계 확인 |
| 시나리오 404 | 서비스 URL과 API path 확인 |
| Compose healthcheck timeout | `docker compose -p ticketing-e2e -f tests/e2e/docker-compose.yml ps`와 각 서비스 로그 확인 |
