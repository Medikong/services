# 테스트 실행 가이드

이 프로젝트의 테스트 진입점은 루트 `Taskfile.yml`이다. 개발자 로컬에는 Docker, Docker Compose, Task를 준비하고, Python pytest, curl, Newman 실행은 컨테이너 안에서 수행한다.

업무 흐름을 사람이 직접 검증하거나 장애를 주입해 확인하는 절차는 배포/인프라 repo에서 별도 문서로 관리한다.

## 테스트 범위

| 구분 | 도구 | 대상 |
| --- | --- | --- |
| 단위 테스트 | Docker Python pytest 러너 | `auth-service`, `patient-service`, `appointment-service`, `prescription-service`, `notification-service` |
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

서비스별 pytest는 각 서비스 디렉터리 안의 `tests/`에 둔다.

```text
services/patient-service/tests/
services/appointment-service/tests/
services/prescription-service/tests/
services/notification-service/tests/
```

## 로컬 단위 테스트

루트에서 전체 서비스 테스트를 실행한다. `task test-unit`은 `tests/docker/Dockerfile`로 Python 테스트 러너 이미지를 빌드한 뒤, 현재 소스 트리를 컨테이너에 마운트해 서비스별 pytest를 실행한다.

```bash
task test-unit
```

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

`.github/workflows/ci.yml`은 `task test-unit`을 실행해 Docker Python 테스트 러너에서 서비스 pytest를 실행한다.

`.github/workflows/e2e.yml`은 push, PR, 수동 실행에서 `task test-e2e`를 실행한다. GitHub runner 안에서 Docker Compose 기반 PostgreSQL/MongoDB/Kafka E2E stack과 Newman을 함께 실행한다.

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
