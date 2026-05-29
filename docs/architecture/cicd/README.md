# CI/CD image build and promotion policy

## 목적

이 문서는 `service` repo에서 Docker image build workflow를 어떤 기준으로 가져갈지 정리한다. 지금 단계의 목표는 배포 자동화가 아니라, 변경된 서비스의 이미지가 언제든 배포 가능한 상태로 빌드되는지 CI에서 확인하는 것이다.

## 기본 방향

- 테스트와 이미지 빌드는 분리한다.
- 테스트 실패는 코드 동작 검증 실패로 본다.
- 이미지 빌드 실패는 Dockerfile, build context, 운영 의존성 조립 실패로 본다.
- 같은 workflow 안에 있더라도 테스트 job과 이미지 빌드 job은 별도 피드백으로 다룬다.
- 이미지 배포, registry 인증, Argo CD sync는 후속 단계에서 다룬다.

## 변경 범위별 빌드 기준

PR에서는 변경된 서비스만 이미지 빌드한다.

- `services/auth-service/**` 변경: `auth-service` 이미지 빌드
- `services/concert-service/**` 변경: `concert-service` 이미지 빌드
- `services/reservation-service/**` 변경: `reservation-service` 이미지 빌드
- `services/payment-service/**` 변경: `payment-service` 이미지 빌드
- `services/ticket-service/**` 변경: `ticket-service` 이미지 빌드
- `services/notification-service/**` 변경: `notification-service` 이미지 빌드
- `dashboard/**` 변경: `dashboard` 이미지 빌드

공통 경로 변경은 안전하게 전체 이미지 빌드로 처리한다.

- `packages/**`
- `Taskfile.yml`
- 이미지 빌드 workflow 파일

이미지 빌드와 직접 관련 없는 변경은 이미지 빌드를 생략한다.

- `tests/**`
- `contracts/**`
- 문서만 변경된 PR

`main` push 또는 수동 실행에서는 `.github/workflows/image-publish.yml`이 publish 단계를 수행한다. 현재 registry endpoint는 GitHub Actions runner 안의 `registry:2`와 `localhost:5000`이며, 전체 또는 선택 이미지를 commit SHA tag로 build -> push -> digest 수집한다. 나중에 영속 registry를 연결할 때는 registry URL과 인증 단계만 교체한다.

## 태그와 산출물

이미지 태그는 기본적으로 commit SHA를 사용한다.

- 기본 태그: `<git-sha>`
- 개발 환경 보조 태그: `dev` 또는 `main`
- 릴리즈 보조 태그: release tag 또는 semantic version

빌드만 수행하는 단계에서는 registry digest가 최종 확정되지 않을 수 있다. registry digest는 push 이후 registry가 반환하는 manifest digest를 기준으로 삼는다. 따라서 이번 단계에서는 이미지 빌드 성공, tag, local image id 또는 build metadata를 남기고, registry push가 붙는 후속 단계에서 digest를 배포 입력값으로 승격한다.

Publish 단계에서는 registry push 후 `RepoDigests` 또는 registry manifest 응답에서 digest를 수집한다. workflow는 `deploy-plan.json`과 `deploy-plan.md`를 `image-publish-deploy-plan` artifact로 남기며, 이 파일에는 image, tag ref, digest ref, source commit, `gitops_action: plan-only`가 포함된다. 이 산출물은 후속 `gitops` repo 업데이트 입력을 검토하기 위한 계획이며, 현재 workflow가 Kubernetes 배포 선언을 직접 수정하지 않는다.

## 환경별 승격 모델

개발 환경은 `main` 기준 지속 배포를 기본 방향으로 둔다.

- `main` merge
- 테스트 통과 여부 확인
- 변경 서비스 이미지 빌드
- registry push
- `gitops` repo의 dev image tag 또는 digest 업데이트
- Argo CD sync

QA와 production은 새 이미지를 다시 빌드하지 않는다. dev 경로에서 만들어진 같은 image digest를 승격한다.

- QA: dev에서 검증된 digest를 QA 환경 값으로 승격
- Production: release tag, 승인, 변경 창, 또는 점진 배포 정책에 따라 같은 digest를 승격

환경별 branch를 오래 유지하는 방식은 기본값으로 삼지 않는다. source repo는 trunk-based 흐름을 유지하고, 환경별 차이는 `gitops` repo의 values 또는 overlay에서 관리한다.

## repo 책임 경계

`service` repo가 소유한다.

- 서비스 코드
- 테스트
- Dockerfile
- image build workflow
- registry push workflow
- tag, image id, digest 산출

`gitops` repo가 소유한다.

- Kubernetes manifest
- Helm values 또는 overlay
- Argo CD Application
- 환경별 image tag 또는 digest 반영
- sync 정책

`infra` repo가 소유한다.

- registry bootstrap
- cluster bootstrap
- cloud substrate
- 네트워크, IAM, 노드 운영 기반

## 이번 작업 범위

이번 CI 작업에서는 PR build 검증과 publish 단계의 책임을 분리한다.

- 변경 서비스 기준 이미지 빌드 matrix 구성
- 공통 경로 변경 시 전체 이미지 빌드
- 테스트 job과 이미지 빌드 job 분리
- commit SHA 기반 tag 사용
- registry push/digest 수집
- 후속 gitops 업데이트에 넘길 deploy-plan artifact 준비

다음 내용은 후속 이슈로 분리한다.

- registry 인증
- 영속 registry push
- `gitops` repo dev values 자동 업데이트
- Argo CD sync
- QA/production promotion workflow

## 참고

- DORA: Continuous delivery, https://dora.dev/capabilities/continuous-delivery/
- DORA: Trunk-based development, https://dora.dev/capabilities/trunk-based-development/
- Argo CD best practices, https://argo-cd.readthedocs.io/en/stable/user-guide/best_practices/
- Argo CD Image Updater strategies, https://argocd-image-updater.readthedocs.io/en/latest/basics/update-strategies/
