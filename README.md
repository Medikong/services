# MediKong Ticketing Services

공연 티켓 예매 플랫폼의 마이크로서비스를 개발하고 검증하는 repo입니다.

이 repo는 서비스 코드, OpenAPI 계약, 단위 테스트, 서비스 이미지 빌드와 registry push를 소유합니다. Kubernetes 배포 선언, Argo CD, Terraform, Ansible, Vagrant, 클러스터 운영 파일은 각 전용 repo에서 관리합니다.

## 구성

| 경로 | 내용 |
| --- | --- |
| `services/auth-service/` | 로그인, JWT 발급, refresh token, 감사 로그 |
| `services/concert-service/` | 공연, 회차, 좌석 재고 조회 |
| `services/reservation-service/` | 좌석 예약, 예약 만료, 예약 이벤트 발행 |
| `services/payment-service/` | 결제 요청, 승인/실패 처리, 결제 이벤트 발행 |
| `services/ticket-service/` | 결제 승인 이벤트 기반 티켓 발급 |
| `services/notification-service/` | Kafka 이벤트 기반 알림 저장 |
| `contracts/` | 서비스별 OpenAPI 문서와 공통 API/JWT 계약 |
| `tests/` | 단위 테스트 러너와 테스트 보조 파일 |

## 서비스 흐름

1. 사용자는 `auth-service`에서 로그인하고 JWT를 발급받습니다.
2. `concert-service`는 공연/회차/좌석 정보를 제공합니다.
3. `reservation-service`는 좌석 예약을 생성하고 예약 이벤트를 발행합니다.
4. `payment-service`는 예약 결제를 처리하고 결제 승인/실패 이벤트를 발행합니다.
5. `ticket-service`는 결제 승인 이벤트를 소비해 티켓을 발급합니다.
6. `notification-service`는 주요 도메인 이벤트를 소비해 알림 이력을 저장합니다.

## 테스트

로컬에는 Docker, Docker Compose, Task가 필요합니다. Python 테스트는 컨테이너 기반 테스트 러너에서 실행합니다.

```bash
task test-unit
task test-service SERVICE=auth-service
```

`task test-unit`은 `tests/docker/Dockerfile` 템플릿으로 서비스별 테스트 러너 이미지를 만든 뒤 티켓 예매 서비스의 pytest를 실행합니다. E2E 테스트는 별도 담당 범위에서 관리합니다.

## 이미지 빌드와 푸시

`service` repo는 Dockerfile과 image build/push 명령을 소유합니다. Kubernetes 배포 선언은 `gitops` repo가 관리하므로, 여기서는 registry와 tag를 인자로 받아 이미지만 준비합니다.

기본 `app-images-*` registry는 VM lab registry인 `10.10.10.10:5000`입니다.

```bash
task app-images-build IMAGE_TAG=dev-split-smoke
task app-images-push IMAGE_TAG=dev-split-smoke
```

Docker Desktop 로컬 개발 루프에서는 VM registry를 쓰지 않고 Docker Desktop용 local registry를 지정합니다. 기본 alias는 `localhost:5001`과 `dev` tag를 사용합니다.

```bash
task dev-images-build
task dev-images-push
```

registry, namespace, tag는 명시적으로 바꿀 수 있습니다.

```bash
task app-images-build IMAGE_REGISTRY=localhost:5001 IMAGE_TAG=dev
task app-images-push IMAGE_REGISTRY=localhost:5001 IMAGE_TAG=dev
task dev-images-push DEV_IMAGE_REGISTRY=localhost:5001 DEV_IMAGE_TAG=dev
task app-images-push IMAGE_REGISTRY=ghcr.io IMAGE_NAMESPACE=owner/service IMAGE_TAG=dev
```

## 제외 범위

다음 책임은 별도 배포/인프라 repo에서 다룹니다.

- Kubernetes manifests, Kustomize overlays, NetworkPolicy, HPA, PDB
- Argo CD Application과 GitOps sync
- Terraform, Ansible, Vagrant, kubeadm cluster 운영
- registry bootstrap, VM bootstrap, cluster apply
