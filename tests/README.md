# 테스트 실행 가이드

이 프로젝트의 테스트 진입점은 루트 `Taskfile.yml`이다. 개발자 로컬에는 Docker, Docker Compose, Task를 준비하고, Python pytest, curl, Newman 실행은 컨테이너 안에서 수행한다.

업무 흐름을 사람이 직접 검증하거나 장애를 주입해 확인하는 절차는 배포/인프라 repo에서 별도 문서로 관리한다.

## 테스트 범위

| 구분 | 도구 | 대상 |
| --- | --- | --- |
| 단위 테스트 | Docker Python pytest 러너 | `auth-service`, `concert-service`, `notification-service`, `payment-service`, `reservation-service`, `ticket-service` |
| E2E 테스트 | Docker Compose, PostgreSQL, MongoDB, Kafka, Docker curl/Newman 컨테이너 | 서비스 DNS 직접 호출로 환자 생성, 예약 확정, 이벤트 발행/소비, 알림 저장, 처방 발행 흐름 |
| Gateway E2E | 별도 future scope | Kong/JWT/Ingress 라우팅과 MetalLB 노출 검증 |

## 폴더 구조

```text
tests/
  docker/
    Dockerfile
  e2e/
    docker-compose.yml
    postman/
      medical-platform.postman_collection.json
    postgres-init/
      01-create-databases.sql
    newman/
      docker.postman_environment.json
    scripts/
      wait-for-services.sh
```

서비스별 pytest는 각 서비스 디렉터리 안의 `tests/`에 둔다. 테스트 실행 Task 본문은 `tests/Taskfile.yml`에 두고, 루트 `Taskfile.yml`은 같은 명령 이름으로 위임한다.

```text
services/auth-service/tests/
services/concert-service/tests/
services/notification-service/tests/
services/payment-service/tests/
services/reservation-service/tests/
services/ticket-service/tests/
```

## 로컬 단위 테스트

루트에서 전체 서비스 테스트를 실행한다. `task test-unit`은 `tests/docker/Dockerfile`로 Python 테스트 러너 이미지를 빌드한 뒤, 현재 소스 트리를 컨테이너에 마운트해 서비스별 pytest를 실행한다.

```bash
task test-unit
```

단일 서비스만 확인할 때는 서비스 전체 이름이나 짧은 이름을 사용할 수 있다.

```bash
task test-service SERVICE=auth-service
task test-service SERVICE=auth
```

여러 서비스만 골라서 확인할 때는 테스트 러너 이미지를 한 번 준비한 뒤 선택된 서비스 테스트를 병렬로 실행한다.

```bash
task test-services SERVICES="auth-service ticket-service"
```

단위 테스트 리포트는 실행할 때마다 `tests/tmp/reports/unit/<service>/` 아래에 서비스별로 생성된다. 실패 원인과 assertion diff는 `pytest.log`에서 확인하고, CI 테스트 요약 도구는 `junit.xml`을 사용할 수 있다. Coverage는 `coverage.xml`과 `htmlcov/`로 남기지만 현재 단계에서는 coverage threshold로 CI를 실패시키지 않는다. 서비스별 `summary.json`과 전체 `tests/tmp/reports/unit/summary.json`에는 테스트 총계, 성공, 실패, 에러, skip, coverage 수치가 기록된다.

## E2E 테스트 흐름

Newman 컬렉션은 Docker Compose 네트워크 DNS로 각 서비스를 직접 호출해 다음 흐름을 검증한다. Kong/JWT/Ingress는 기본 `task test-e2e` 범위가 아니며, 서비스가 기대하는 `X-User-*` 헤더를 요청에 직접 넣는다.

1. `STAFF` 사용자 헤더로 `patient-service`의 `POST /patients`를 호출해 환자를 생성한다.
2. `PATIENT` 사용자 헤더로 `appointment-service`의 `POST /appointments`를 호출해 예약을 요청한다.
3. `DOCTOR` 사용자 헤더로 `POST /appointments/{appointmentId}/confirm`을 호출해 예약을 확정한다.
4. 예약 확정 이벤트가 `appointment-confirmed` 토픽으로 발행되고 `notification-service`가 알림을 저장한다.
5. `DOCTOR` 사용자 헤더로 `prescription-service`의 `POST /prescriptions`를 호출해 처방을 발행한다.
6. 처방 발행 이벤트가 `prescription-issued` 토픽으로 발행되고 `notification-service`가 알림을 저장한다.
7. `PATIENT` 사용자 헤더로 `GET /notifications`, `GET /prescriptions`를 호출해 본인 데이터가 조회되는지 확인한다.

## 로컬 E2E 실행

`task test-e2e`는 Docker Compose로 PostgreSQL, MongoDB, Kafka, FastAPI 서비스를 띄운 뒤 같은 Compose 네트워크에서 Newman을 실행한다. 서비스 URL은 Compose DNS 이름을 사용한다.

```bash
task test-e2e
```

기본 URL은 다음과 같다.

| 서비스 | 기본 URL |
| --- | --- |
| `patient-service` | `http://patient-service:8081` |
| `appointment-service` | `http://appointment-service:8082` |
| `prescription-service` | `http://prescription-service:8083` |
| `notification-service` | `http://notification-service:8084` |

`tests/e2e/scripts/wait-for-services.sh`는 Docker curl 컨테이너 안에서 실행된다. Newman 컬렉션도 Docker Newman 컨테이너 안에서 실행되므로 로컬에 curl이나 newman을 따로 설치하지 않는다.

수동으로 stack을 살펴보려면 다음 명령을 사용한다.

```bash
task e2e-up
task e2e-wait
task e2e-newman
task e2e-down
```

## CI

`.github/workflows/ci.yml`은 PR 변경 경로를 기준으로 테스트 대상과 Docker image 빌드 대상을 따로 만든다. 테스트 job은 `task test-services SERVICES="<services>"`를 한 번 실행하고, 이미지 빌드 job은 `.github/workflows/image-build.yml` 재사용 workflow를 호출한다. 재사용 workflow는 선택된 image matrix마다 `task app-image-build SERVICE=<image> IMAGE_REGISTRY=ci.local IMAGE_TAG=<commit-sha>`를 실행한다. 두 job은 분리되어 있어 테스트 실패와 Dockerfile/build context 실패가 별도 피드백으로 보인다.

`services/<service>/**` 변경은 해당 서비스 테스트와 이미지를 선택하고, `dashboard/**` 변경은 `dashboard` 이미지만 선택한다. `tests/**` 변경은 전체 서비스 테스트만 실행하며, `packages/**`, `Taskfile.yml`, `.github/workflows/ci.yml` 변경은 전체 서비스 테스트와 전체 이미지 빌드를 실행한다. `.github/workflows/image-build.yml` 또는 `.github/workflows/image-publish.yml` 변경은 전체 이미지 빌드를 실행한다. `contracts/**`나 문서만 변경된 PR은 서비스 테스트와 이미지 빌드 모두 no-op 성공 job으로 끝난다. `main` push의 registry publish는 `.github/workflows/image-publish.yml`이 담당한다.

`.github/workflows/image-publish.yml`은 `main` push 또는 수동 실행에서 GitHub Actions runner 안의 `registry:2`를 현재 publish registry인 `localhost:5000`으로 띄운다. 각 image는 commit SHA tag로 `task app-image-build SERVICE=<image> IMAGE_REGISTRY=localhost:5000 IMAGE_TAG=<commit-sha>`를 실행한 뒤 push하고, registry digest를 수집해 `image-publish-deploy-plan` artifact와 workflow summary에 남긴다. 나중에 영속 registry를 연결할 때는 registry URL과 인증 단계만 교체한다. 이 산출물은 후속 `gitops` repo image tag/digest 업데이트의 입력 계획이며, 현재 단계에서는 Kubernetes 배포 선언 수정이나 Argo CD sync를 수행하지 않는다.

CI는 단위 테스트 성공/실패와 관계없이 `unit-test-reports` artifact를 업로드한다. artifact 안의 `tests/tmp/reports/unit/<service>/summary.json`에는 서비스명, 성공/실패 상태, exit code, 시작/종료 시각, 실행 시간과 테스트 메트릭이 기록된다. GitHub Actions summary에는 `tests/tmp/reports/unit/summary.md`의 전체 단위 테스트 표가 표시된다.

`.github/workflows/e2e.yml`은 `main` push와 수동 실행에서만 `task test-e2e`를 실행한다. GitHub runner 안에서 Docker Compose 기반 PostgreSQL/MongoDB/Kafka E2E stack과 Newman을 함께 실행한다.

Kong/JWT/Ingress 검증은 기본 E2E와 분리한다. 이후 필요해지면 `task test-gateway-e2e` 같은 별도 타깃에서 MetalLB IP 또는 Ingress 주소, JWT 생성, Gateway 라우팅 검증을 다룬다.

## 실패 시 점검 포인트

| 증상 | 점검 |
| --- | --- |
| Docker build 실패 | Docker Desktop/Engine 실행 상태 확인 |
| `docker compose` 실패 | Docker Compose plugin 설치 여부 확인 |
| pytest import 실패 | `task test-unit`로 Docker 테스트 러너를 통해 실행했는지 확인 |
| DB 연결 실패 | `DATABASE_URL` 값과 PostgreSQL 실행 상태 확인 |
| Kafka 이벤트 검증 실패 | Compose `kafka:29092`, topic auto-create, `notification-service` consumer 로그 확인 |
| Newman 401 | `X-User-Id`, `X-User-Role` 헤더 누락 여부 확인 |
| Newman 403 | `X-Patient-Id`, `X-Doctor-Id`와 요청 데이터의 권한 관계 확인 |
| Newman 404 | 서비스 URL과 API path 확인 |
| Newman readiness timeout | `docker compose -p medical-platform-e2e -f tests/e2e/docker-compose.yml ps`와 각 서비스 로그 확인 |
