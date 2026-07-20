#!/usr/bin/env bash
set -euo pipefail

test_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${test_dir}/../../.." && pwd)"
script_path="${repo_root}/.github/scripts/send-discord-deploy.sh"
template_path="${repo_root}/.github/notifications/discord/deploy-result.json"
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
  local publish_result="$1"
  local deploy_result="$2"
  local publish_dir="$3"
  local deploy_file="$4"
  local output="$5"

  SELECT_RESULT=success \
  PUBLISH_RESULT="${publish_result}" \
  DEPLOY_RESULT="${deploy_result}" \
  SELECTED_IMAGES_JSON="${selected_images}" \
  DEPLOY_ENVIRONMENT=dev \
  DEPLOY_TARGET=changed \
  DEPLOY_TAG=deploy/dev/changed/1.2.3-test \
  GITHUB_SERVER_URL=https://github.com \
  GITHUB_REPOSITORY=DropMong/services \
  GITHUB_RUN_ID=1 \
  GITHUB_SHA=1234567890abcdef \
  GITHUB_ACTOR=dropmong-user \
    bash "${script_path}" \
      --template "${template_path}" \
      --publish-results-dir "${publish_dir}" \
      --deploy-result-file "${deploy_file}" \
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
jq -n '{status:"success",stage:"complete",reason:"GitOps 반영 완료"}' \
  > "${temp_dir}/success/deploy-result.json"

success_payload="${temp_dir}/success/payload.json"
run_renderer success success "${success_dir}" "${temp_dir}/success/deploy-result.json" "${success_payload}"

jq -e '
  .embeds[0].title == "✅ DropMong Deploy · GitOps 반영 완료"
  and .embeds[0].color == 3066993
  and (.embeds[0].description | contains("2개 서비스"))
  and ([.. | strings | select(contains("{{"))] | length == 0)
  and ([.embeds[0].fields[].value | length <= 1024] | all)
  and ([.embeds[0].fields[] | select(.name == "서비스 결과") | .value][0] | contains("auth-service"))
  and ([.embeds[0].fields[] | select(.name == "서비스 결과") | .value][0] | contains("order-service"))
' "${success_payload}" >/dev/null

failure_dir="${temp_dir}/failure/publish-results"
mkdir -p "${failure_dir}"
write_publish_result \
  "${failure_dir}/auth-service.json" auth-service v1.2.3 failure "Trivy CRITICAL 취약점 검사 실패"
write_publish_result \
  "${failure_dir}/order-service.json" order-service v2.3.4 success "이미지 publish 완료" \
  "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

failure_payload="${temp_dir}/failure/payload.json"
run_renderer failure skipped "${failure_dir}" "${temp_dir}/failure/missing-deploy-result.json" "${failure_payload}"

jq -e '
  .embeds[0].title == "❌ DropMong Deploy · 배포 준비 실패"
  and .embeds[0].color == 15158332
  and ([.embeds[0].fields[] | select(.name == "실패 원인") | .value][0] | contains("auth-service"))
  and ([.embeds[0].fields[] | select(.name == "실패 원인") | .value][0] | contains("Trivy CRITICAL"))
  and (
    [.embeds[0].fields[] | select(.name == "현재 단계") | .value][0]
    | contains("GitOps 반영 미실행")
  )
  and ([.embeds[0].fields[] | select(.name == "서비스 결과") | .value][0] | contains("order-service"))
  and ([.embeds[0].fields[] | select(.name == "서비스 결과") | .value][0] | contains("GitOps 미반영"))
' "${failure_payload}" >/dev/null

printf 'Discord deploy Bash 테스트 통과\n'
