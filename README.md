# MediKong Services

FastAPI 기반 의료 MSA 서비스와 정적 dashboard를 개발하고 검증하는 repo입니다.

이 repo의 책임은 서비스 개발, 단위 테스트, Docker Compose E2E 테스트, Docker image build, registry push까지입니다. Kubernetes 배포 선언, Argo CD, Terraform, Ansible, Vagrant, 클러스터 운영 파일은 이 repo에 두지 않습니다.

## 구성

| 경로 | 내용 |
| --- | --- |
| `services/auth-service/` | 로그인, JWT 발급, 감사 로그 |
| `services/patient-service/` | 환자 정보와 의료 요약 |
| `services/appointment-service/` | 예약 요청, 확정, 취소와 예약 이벤트 발행 |
| `services/prescription-service/` | 처방 발행, 조회와 처방 이벤트 발행 |
| `services/notification-service/` | Kafka 이벤트 기반 알림 저장 |
| `dashboard/` | 서비스 동작을 확인하는 정적 화면 |
| `tests/` | Docker pytest runner와 Docker Compose E2E 테스트 |

## 테스트

로컬에는 Docker, Docker Compose, Task가 필요합니다. Python과 Newman은 컨테이너 안에서 실행합니다.

```bash
task test-unit
task test-e2e
```

`task test-unit`은 `tests/docker/Dockerfile`로 테스트 러너 이미지를 만든 뒤 각 서비스의 pytest를 실행합니다. `task test-e2e`는 PostgreSQL, MongoDB, Kafka, FastAPI 서비스를 Docker Compose로 올리고 Newman collection을 실행합니다.

## 이미지 빌드와 푸시

`service` repo는 Dockerfile과 image build/push 명령을 소유합니다. Kubernetes 배포 선언은 `gitops` repo가 관리하므로, 여기서는 registry와 tag를 인자로 받아 이미지만 준비합니다.

Python 서비스 이미지는 단일 바이너리 산출물이 아니라 운영 의존성만 담은 `/opt/venv` 기반 멀티 스테이지 이미지로 구성합니다.

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

자세한 테스트 흐름은 `tests/README.md`를 참고합니다.
