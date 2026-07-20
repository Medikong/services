#!/usr/bin/env bash
set -euo pipefail

SUCCESS_COLOR=3066993
FAILURE_COLOR=15158332
DISCORD_FIELD_LIMIT=1024
template_path=""
publish_results_dir=""
output_path=""
dry_run=false

usage() {
  printf '%s\n' \
    'Usage: send-discord-image-publish.sh --template PATH --publish-results-dir PATH' \
    '       --output PATH [--dry-run]'
}

while (($# > 0)); do
  case "$1" in
    --template) template_path="${2:-}"; shift 2 ;;
    --publish-results-dir) publish_results_dir="${2:-}"; shift 2 ;;
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

if [[ -z "${template_path}" || -z "${publish_results_dir}" || -z "${output_path}" ]]; then
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
success_count=0
failure_count=0
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
    failure_count=$((failure_count + 1))
  else
    status="$(jq -r '.status' <<<"${result}")"
    reason="$(jq -r '.reason' <<<"${result}")"
    if [[ "${status}" == "success" ]]; then
      digest="$(short_digest "$(jq -r '.digest' <<<"${result}")")"
      printf -v line '✅ `%s:%s` · `%s`' "${image_name}" "${image_tag}" "${digest}"
      failure_line=""
      success_count=$((success_count + 1))
    else
      printf -v line '❌ `%s:%s` · %s' "${image_name}" "${image_tag}" "${reason}"
      printf -v failure_line '`%s`: %s' "${image_name}" "${reason}"
      failure_count=$((failure_count + 1))
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
service_count="$(jq 'length' <<<"${selected_images_json}")"

failure_reasons=""
if [[ "${SELECT_RESULT:-}" != "success" ]]; then
  failure_reasons="- Git 태그 해석 또는 이미지 선택 단계가 실패했습니다."
elif [[ -n "${service_failures}" ]]; then
  failure_reasons="${service_failures}"
elif ((success_count != service_count)); then
  failure_reasons="- 이미지 빌드, 보안 검사 또는 ECR 푸시 결과를 확인할 수 없습니다."
fi

if [[ "${SELECT_RESULT:-}" != "success" ]]; then
  title="❌ DropMong · ECR 이미지 선택 실패"
  description="Git 태그를 해석하지 못해 ECR 이미지 푸시를 시작하지 않았습니다."
  failure_reason="$(truncate_field "${failure_reasons}")"
  color="${FAILURE_COLOR}"
elif [[ -z "${failure_reasons}" ]]; then
  title="📦 DropMong · ECR 이미지 푸시 완료"
  description="컨테이너 이미지 ${success_count}개를 ECR에 푸시했습니다."
  failure_reason=""
  color="${SUCCESS_COLOR}"
else
  title="❌ DropMong · ECR 이미지 푸시 실패"
  description="대상 ${service_count}개 중 성공 ${success_count}개, 실패 ${failure_count}개입니다. GitHub Actions 로그를 확인해 주세요."
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
printf -v registry_value '`%s`' "${IMAGE_REGISTRY:-확인 불가}"
printf -v source_tag_value '`%s`' "${SOURCE_TAG:-확인 불가}"
printf -v source_sha_value '`%s`' "${source_sha:-확인 불가}"
printf -v actor_value '`%s`' "${GITHUB_ACTOR:-확인 불가}"
timestamp="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"

context_json="$(
  jq -cn \
    --arg title "${title}" \
    --arg description "${description}" \
    --arg run_url "${run_url}" \
    --argjson color "${color}" \
    --argjson service_count "${service_count}" \
    --arg services "${services}" \
    --arg failure_reason "${failure_reason}" \
    --arg registry "${registry_value}" \
    --arg source_tag "${source_tag_value}" \
    --arg source_sha "${source_sha_value}" \
    --arg actor "${actor_value}" \
    --arg timestamp "${timestamp}" \
    '{
      title: $title, description: $description, run_url: $run_url, color: $color,
      service_count: $service_count, services: $services,
      failure_reason: $failure_reason, registry: $registry, source_tag: $source_tag,
      source_sha: $source_sha, actor: $actor, timestamp: $timestamp
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
  | (.embeds[].fields) |= map(select(.value != ""))
' "${template_path}" > "${output_path}"

if ! jq -e '[.. | strings | select(contains("{{"))] | length == 0' "${output_path}" >/dev/null; then
  printf 'Discord 템플릿에 치환되지 않은 값이 남아 있습니다.\n' >&2
  exit 1
fi
if [[ "${dry_run}" == "true" ]]; then
  exit 0
fi
if [[ -z "${DISCORD_IMAGE_WEBHOOK:-}" ]]; then
  printf 'Discord 이미지 알림용 GitHub Secret이 설정되지 않았습니다.\n' >&2
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
  "${DISCORD_IMAGE_WEBHOOK}" \
  >/dev/null
