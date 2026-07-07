# MediKong DropMong Services

DropMong 한정 상품 드롭 커머스의 마이크로서비스를 개발하고 검증하는 repo입니다.

이 repo는 서비스 코드, OpenAPI 계약, 단위 테스트, 서비스 이미지 빌드와 registry push를 소유합니다. Kubernetes 배포 선언, Argo CD, Terraform, Ansible, Vagrant, 클러스터 운영 파일은 각 전용 repo에서 관리합니다.

## 구성

| 경로 | 내용 |
| --- | --- |
| `services/catalog-service/` | 상품, 드롭 목록, 드롭 상세 조회 |
| `services/order-service/` | 주문 생성, 재고 예약, 주문 확정, 주문 이벤트 발행 |
| `services/payment-service/` | mock 결제 승인, 결제 이벤트 발행 |
| `services/notification-service/` | Kafka 이벤트 기반 알림 저장과 조회 |
| `contracts/` | 서비스별 OpenAPI 문서와 공통 API/JWT 계약 |
| `tests/` | 단위 테스트 러너와 테스트 보조 파일 |

`auth-service`는 JWT 발급 경계로 남기되, 정상 구매 1차 구현에서는 Istio Gateway가 검증한 사용자 context가 전달된다는 전제로 시작한다.

## 서비스 흐름

DropMong은 로그인된 사용자가 한정 상품 드롭을 발견하고, 오픈 시각에 구매를 시도한 뒤 결제 결과와 알림까지 확인하는 흐름을 기준으로 서비스를 나눕니다.

1. 사용자는 공개된 드롭 목록에서 한정 상품을 발견하고 상세 정보를 확인합니다.
2. 오픈 시간이 되면 사용자는 구매를 시도하고, 시스템은 구매 가능 여부를 판단합니다.
3. 구매 가능하면 사용자는 결제를 진행하고, 결제 성공 시 구매 결과를 확인합니다.
4. 준비된 수량이 모두 소진되었거나 요청이 몰리면 사용자는 품절, 대기, 재시도 안내를 받습니다.
5. 결제가 실패하거나 지연되면 사용자는 실패, 확인 중, 만료 같은 상태를 확인합니다.
6. 구매 성공, 실패, 품절, 쿠폰 지급 같은 결과는 알림으로 확인할 수 있습니다.
7. 운영자는 상품과 드롭 조건을 준비하고, 오픈 이후 판매 상태와 장애 상황을 확인합니다.

## 테스트

로컬에는 Docker, Docker Compose, Task가 필요합니다. Python 테스트는 컨테이너 기반 테스트 러너에서 실행합니다.

```bash
task test-unit
task test-service SERVICE=catalog-service
```

`task test-unit`은 `tests/docker/Dockerfile` 템플릿으로 서비스별 테스트 러너 이미지를 만든 뒤 DropMong 서비스의 pytest를 실행합니다. E2E 테스트는 Docker Compose와 Newman 기반으로 정상 구매 흐름을 검증합니다.

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
