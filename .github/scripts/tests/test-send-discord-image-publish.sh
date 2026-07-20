#!/usr/bin/env bash
set -euo pipefail

test_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${test_dir}/../../.." && pwd)"
script_path="${repo_root}/.github/scripts/send-discord-image-publish.sh"
template_path="${repo_root}/.github/notifications/discord/image-publish-result.json"
temp_dir="$(mktemp -d)"
trap 'rm -rf "${temp_dir}"' EXIT

selected_images='[
  {"image":"auth-service","tag":"v1.2.3"},
  {"image":"order-service","tag":"v2.3.4"}
]'

write_publish_result() {
  local output="$1"
  local image="$2"
  local tag="$3"
  local status="$4"
  local reason="$5"
  local digest="${6:-}"
  jq -n \
    --arg image "${image}" \
    --arg tag "${tag}" \
    --arg status "${status}" \
    --arg reason "${reason}" \
    --arg digest "${digest}" \
    '{image: $image, tag: $tag, status: $status, stage: "test", reason: $reason, digest: $digest}' \
    > "${output}"
}

run_renderer() {
  local publish_dir="$1"
  local output="$2"

  SELECT_RESULT=success \
  SELECTED_IMAGES_JSON="${selected_images}" \
  IMAGE_REGISTRY=205623789422.dkr.ecr.ap-northeast-2.amazonaws.com \
  SOURCE_TAG=deploy/dev/changed/1.2.3-test \
  GITHUB_SERVER_URL=https://github.com \
  GITHUB_REPOSITORY=DropMong/services \
  GITHUB_RUN_ID=1 \
  GITHUB_SHA=1234567890abcdef \
  GITHUB_ACTOR=dropmong-user \
    bash "${script_path}" \
      --template "${template_path}" \
      --publish-results-dir "${publish_dir}" \
      --output "${output}" \
      --dry-run
}

success_dir="${temp_dir}/success/publish-results"
mkdir -p "${success_dir}"
write_publish_result \
  "${success_dir}/auth-service.json" auth-service v1.2.3 success "이미지 publish 완료" \
  "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
write_publish_result \
  "${success_dir}/order-service.json" order-service v2.3.4 success "이미지 publish 완료" \
  "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
success_payload="${temp_dir}/success/payload.json"
run_renderer "${success_dir}" "${success_payload}"

jq -e '
  .username == "DropMong Image"
  and .embeds[0].title == "📦 DropMong · ECR 이미지 푸시 완료"
  and .embeds[0].color == 3066993
  and (.embeds[0].description | contains("이미지 2개"))
  and ([.. | strings | select(contains("{{"))] | length == 0)
  and ([.embeds[0].fields[].value | length <= 1024] | all)
  and ([.embeds[0].fields[] | select(.name == "이미지") | .value][0] | contains("auth-service:v1.2.3"))
  and ([.embeds[0].fields[] | select(.name == "이미지") | .value][0] | contains("order-service:v2.3.4"))
  and ([.embeds[0].fields[] | select(.name == "ECR 레지스트리") | .value][0] | contains("205623789422.dkr.ecr.ap-northeast-2.amazonaws.com"))
  and ([.embeds[0].fields[] | select(.name == "실패 원인")] | length == 0)
  and ([.embeds[0].fields[] | select(.name == "환경")] | length == 0)
' "${success_payload}" >/dev/null

failure_dir="${temp_dir}/failure/publish-results"
mkdir -p "${failure_dir}"
write_publish_result \
  "${failure_dir}/auth-service.json" auth-service v1.2.3 failure "Trivy CRITICAL 취약점 검사 실패"
write_publish_result \
  "${failure_dir}/order-service.json" order-service v2.3.4 success "이미지 publish 완료" \
  "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

failure_payload="${temp_dir}/failure/payload.json"
run_renderer "${failure_dir}" "${failure_payload}"

jq -e '
  .embeds[0].title == "❌ DropMong · ECR 이미지 푸시 실패"
  and .embeds[0].color == 15158332
  and ([.embeds[0].fields[] | select(.name == "실패 원인") | .value][0] | contains("auth-service"))
  and ([.embeds[0].fields[] | select(.name == "실패 원인") | .value][0] | contains("Trivy CRITICAL"))
  and ([.embeds[0].fields[] | select(.name == "이미지") | .value][0] | contains("order-service:v2.3.4"))
  and ([.embeds[0].fields[] | select(.name == "이미지") | .value][0] | contains("GitOps") | not)
' "${failure_payload}" >/dev/null

selection_failure_payload="${temp_dir}/selection-failure/payload.json"
SELECT_RESULT=failure \
SELECTED_IMAGES_JSON="" \
IMAGE_REGISTRY=205623789422.dkr.ecr.ap-northeast-2.amazonaws.com \
SOURCE_TAG=deploy/dev/invalid/v0.0.1 \
GITHUB_SERVER_URL=https://github.com \
GITHUB_REPOSITORY=DropMong/services \
GITHUB_RUN_ID=2 \
GITHUB_SHA=1234567890abcdef \
GITHUB_ACTOR=dropmong-user \
  bash "${script_path}" \
    --template "${template_path}" \
    --publish-results-dir "${temp_dir}/selection-failure/missing-results" \
    --output "${selection_failure_payload}" \
    --dry-run

jq -e '
  .embeds[0].title == "❌ DropMong · ECR 이미지 선택 실패"
  and (.embeds[0].description | contains("푸시를 시작하지 않았습니다"))
  and ([.embeds[0].fields[] | select(.name == "실패 원인") | .value][0] | contains("Git 태그"))
' "${selection_failure_payload}" >/dev/null

printf 'Discord ECR 이미지 Bash 테스트 통과\n'
