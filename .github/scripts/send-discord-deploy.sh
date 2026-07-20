#!/usr/bin/env bash
set -euo pipefail

SUCCESS_COLOR=3066993
FAILURE_COLOR=15158332
DISCORD_FIELD_LIMIT=1024
template_path=""
publish_results_dir=""
deploy_result_file=""
output_path=""
dry_run=false

usage() {
  printf '%s\n' \
    'Usage: send-discord-deploy.sh --template PATH --publish-results-dir PATH' \
    '       --deploy-result-file PATH --output PATH [--dry-run]'
}

while (($# > 0)); do
  case "$1" in
    --template) template_path="${2:-}"; shift 2 ;;
    --publish-results-dir) publish_results_dir="${2:-}"; shift 2 ;;
    --deploy-result-file) deploy_result_file="${2:-}"; shift 2 ;;
    --output) output_path="${2:-}"; shift 2 ;;
    --dry-run) dry_run=true; shift ;;
    --help|-h) usage; exit 0 ;;
    *)
      printf '지원하지 않는 인자입니다: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ -z "${template_path}" || -z "${publish_results_dir}" || -z "${deploy_result_file}" || -z "${output_path}" ]]; then
  usage >&2
  exit 2
fi
if [[ ! -f "${template_path}" ]]; then
  printf 'Discord 템플릿을 찾을 수 없습니다: %s\n' "${template_path}" >&2
  exit 1
fi
if ! command -v jq >/dev/null 2>&1; then
  printf 'jq가 필요합니다.\n' >&2
  exit 1
fi

truncate_field() {
  local value="$1"
  local suffix=$'\n… 자세한 내용은 GitHub Actions 로그를 확인하세요.'
  local prefix_length
  if ((${#value} <= DISCORD_FIELD_LIMIT)); then
    printf '%s' "${value}"
    return
  fi
  prefix_length=$((DISCORD_FIELD_LIMIT - ${#suffix}))
  printf '%s%s' "${value:0:${prefix_length}}" "${suffix}"
}

short_digest() {
  local digest="$1"
  if [[ "${digest}" == sha256:* ]] && ((${#digest} > 15)); then
    printf '%s' "${digest:0:15}"
  elif [[ -n "${digest}" ]]; then
    printf '%s' "${digest}"
  else
    printf 'digest 없음'
  fi
}

if ! selected_images_json="$(
  printf '%s' "${SELECTED_IMAGES_JSON:-}" |
    jq -ce 'if type == "array" then [.[] | select(type == "object" and (.image | type == "string") and (.tag | type == "string"))] else [] end' 2>/dev/null
)"; then
  selected_images_json='[]'
fi

publish_results_json='[]'
if [[ -d "${publish_results_dir}" ]]; then
  while IFS= read -r -d '' result_file; do
    result_json="$(
      jq -c '
        select(type == "object" and (.image | type == "string") and (.image | length > 0))
        | {
            image,
            tag: (.tag // "unknown" | tostring),
            status: (.status // "failure" | tostring),
            reason: (.reason // "결과를 확인할 수 없습니다." | tostring),
            digest: (.digest // "" | tostring)
          }
      ' "${result_file}" 2>/dev/null || true
    )"
    if [[ -n "${result_json}" ]]; then
      publish_results_json="$(
        jq -cn --argjson current "${publish_results_json}" --argjson item "${result_json}" \
          '$current + [$item]'
      )"
    fi
  done < <(find "${publish_results_dir}" -type f -name '*.json' -print0)
fi

services=""
service_failures=""
while IFS= read -r selected; do
  image_name="$(jq -r '.image' <<<"${selected}")"
  image_tag="$(jq -r '.tag' <<<"${selected}")"
  result="$(
    jq -c --arg image "${image_name}" \
      'map(select(.image == $image)) | last // empty' \
      <<<"${publish_results_json}"
  )"

  if [[ -z "${result}" ]]; then
    printf -v line '❌ `%s` · `%s` · 결과 파일 없음' "${image_name}" "${image_tag}"
    printf -v failure_line '`%s`: 이미지 publish 결과 파일을 확인할 수 없습니다.' "${image_name}"
  else
    status="$(jq -r '.status' <<<"${result}")"
    reason="$(jq -r '.reason' <<<"${result}")"
    if [[ "${status}" == "success" ]]; then
      digest="$(short_digest "$(jq -r '.digest' <<<"${result}")")"
      if [[ "${PUBLISH_RESULT:-}" == "success" ]]; then
        printf -v line '✅ `%s` · `%s` · `%s`' "${image_name}" "${image_tag}" "${digest}"
      else
        printf -v line '⚠️ `%s` · `%s` · 이미지 게시 완료, GitOps 미반영' "${image_name}" "${image_tag}"
      fi
      failure_line=""
    else
      printf -v line '❌ `%s` · `%s` · %s' "${image_name}" "${image_tag}" "${reason}"
      printf -v failure_line '`%s`: %s' "${image_name}" "${reason}"
    fi
  fi

  [[ -z "${services}" ]] || services+=$'\n'
  services+="${line}"
  if [[ -n "${failure_line}" ]]; then
    [[ -z "${service_failures}" ]] || service_failures+=$'\n'
    service_failures+="- ${failure_line}"
  fi
done < <(jq -c '.[]' <<<"${selected_images_json}")

[[ -n "${services}" ]] || services="서비스 결과를 확인할 수 없습니다."
services="$(truncate_field "${services}")"

failure_reasons=""
if [[ "${SELECT_RESULT:-}" != "success" ]]; then
  failure_reasons="- deploy tag 해석 또는 이미지 선택 단계가 실패했습니다."
  current_stage="이미지 선택 실패 · 배포 중단"
elif [[ "${PUBLISH_RESULT:-}" != "success" ]]; then
  failure_reasons="${service_failures:-- 이미지 build, 보안 검사 또는 ECR publish 단계가 실패했습니다.}"
  current_stage="이미지 publish 실패 · GitOps 반영 미실행"
elif [[ -n "${service_failures}" ]]; then
  failure_reasons="${service_failures}"
  current_stage="이미지 publish 결과 확인 실패 · GitOps 반영 상태 확인 필요"
elif [[ "${DEPLOY_RESULT:-}" != "success" ]]; then
  deploy_reason=""
  if [[ -f "${deploy_result_file}" ]]; then
    deploy_reason="$(jq -r '.reason // "GitOps 반영 결과를 확인할 수 없습니다."' "${deploy_result_file}" 2>/dev/null || true)"
  fi
  failure_reasons="- ${deploy_reason:-deploy plan 조립 또는 GitOps 저장소 반영 단계가 실패했습니다.}"
  current_stage="GitOps 반영 실패 · Argo CD 반영 전"
else
  current_stage="GitOps 반영 완료 · Argo CD 동기화 대기"
fi

service_count="$(jq 'length' <<<"${selected_images_json}")"
if [[ -z "${failure_reasons}" ]]; then
  title="✅ DropMong Deploy · GitOps 반영 완료"
  description="${service_count}개 서비스의 이미지 게시와 GitOps 배포 값 반영이 완료되었습니다."
  failure_reason="해당 없음"
  color="${SUCCESS_COLOR}"
else
  title="❌ DropMong Deploy · 배포 준비 실패"
  description="서비스 이미지 게시 또는 GitOps 반영 과정이 실패했습니다. 실패 원인과 GitHub Actions 로그를 확인해 주세요."
  failure_reason="$(truncate_field "${failure_reasons}")"
  color="${FAILURE_COLOR}"
fi

github_server_url="${GITHUB_SERVER_URL:-https://github.com}"
if [[ -n "${GITHUB_REPOSITORY:-}" && -n "${GITHUB_RUN_ID:-}" ]]; then
  run_url="${github_server_url}/${GITHUB_REPOSITORY}/actions/runs/${GITHUB_RUN_ID}"
else
  run_url="${github_server_url}"
fi
source_sha="${GITHUB_SHA:-}"
source_sha="${source_sha:0:8}"
printf -v environment_value '`%s`' "${DEPLOY_ENVIRONMENT:-확인 불가}"
printf -v target_value '`%s`' "${DEPLOY_TARGET:-확인 불가}"
printf -v deploy_tag_value '`%s`' "${DEPLOY_TAG:-확인 불가}"
printf -v source_sha_value '`%s`' "${source_sha:-확인 불가}"
printf -v actor_value '`%s`' "${GITHUB_ACTOR:-확인 불가}"
printf -v current_stage_value '`%s`' "${current_stage}"
timestamp="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"

context_json="$(
  jq -cn \
    --arg title "${title}" \
    --arg description "${description}" \
    --arg run_url "${run_url}" \
    --argjson color "${color}" \
    --arg environment "${environment_value}" \
    --arg target "${target_value}" \
    --argjson service_count "${service_count}" \
    --arg services "${services}" \
    --arg failure_reason "${failure_reason}" \
    --arg deploy_tag "${deploy_tag_value}" \
    --arg source_sha "${source_sha_value}" \
    --arg actor "${actor_value}" \
    --arg current_stage "${current_stage_value}" \
    --arg timestamp "${timestamp}" \
    '{
      title: $title, description: $description, run_url: $run_url, color: $color,
      environment: $environment, target: $target, service_count: $service_count,
      services: $services, failure_reason: $failure_reason, deploy_tag: $deploy_tag,
      source_sha: $source_sha, actor: $actor, current_stage: $current_stage,
      timestamp: $timestamp
    }'
)"

mkdir -p "$(dirname "${output_path}")"
jq --argjson context "${context_json}" '
  def render($context):
    if type == "object" then
      with_entries(.value |= render($context))
    elif type == "array" then
      map(render($context))
    elif type == "string" then
      . as $original
      | if test("^\\{\\{[[:space:]]*[a-z_]+[[:space:]]*\\}\\}$") then
          capture("^\\{\\{[[:space:]]*(?<key>[a-z_]+)[[:space:]]*\\}\\}$").key as $key
          | if $context | has($key) then $context[$key] else $original end
        else
          reduce ($context | to_entries[]) as $entry (
            $original;
            gsub("\\{\\{[[:space:]]*" + $entry.key + "[[:space:]]*\\}\\}"; ($entry.value | tostring))
          )
        end
    else
      .
    end;
  render($context)
' "${template_path}" > "${output_path}"

if ! jq -e '[.. | strings | select(contains("{{"))] | length == 0' "${output_path}" >/dev/null; then
  printf 'Discord 템플릿에 치환되지 않은 값이 남아 있습니다.\n' >&2
  exit 1
fi
if [[ "${dry_run}" == "true" ]]; then
  exit 0
fi
if [[ -z "${DISCORD_DEPLOY_WEBHOOK:-}" ]]; then
  printf 'DISCORD_DEPLOY_WEBHOOK GitHub Secret이 설정되지 않았습니다.\n' >&2
  exit 1
fi
if ! command -v curl >/dev/null 2>&1; then
  printf 'curl이 필요합니다.\n' >&2
  exit 1
fi

curl --fail --silent --show-error \
  --header 'Content-Type: application/json' \
  --header 'User-Agent: DropMong-GitHub-Actions/1.0' \
  --data-binary "@${output_path}" \
  "${DISCORD_DEPLOY_WEBHOOK}" \
  >/dev/null
