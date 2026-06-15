#!/usr/bin/env bash
set -euo pipefail

DEPLOY_ENV="${DEPLOY_ENV:-dev}"
SERVICE="${SERVICE:-}"
BUMP="${BUMP:-patch}"
DRY_RUN="${DRY_RUN:-false}"

SERVICES=(
  auth-service
  concert-service
  reservation-service
  payment-service
  ticket-service
  notification-service
)

usage() {
  cat >&2 <<'EOF'
Usage:
  task deploy:tag SERVICE=<service|changed|all> BUMP=<patch|minor|major> [DRY_RUN=true]

SERVICE:
  auth-service | concert-service | reservation-service | payment-service |
  ticket-service | notification-service | changed | all
EOF
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf 'required command not found: %s\n' "$1" >&2
    exit 2
  fi
}

is_service() {
  local candidate="$1"
  local service
  for service in "${SERVICES[@]}"; do
    if [ "${candidate}" = "${service}" ]; then
      return 0
    fi
  done
  return 1
}

validate_inputs() {
  if ! is_service "${SERVICE}" && [ "${SERVICE}" != "changed" ] && [ "${SERVICE}" != "all" ]; then
    usage
    printf 'invalid SERVICE: %s\n' "${SERVICE}" >&2
    exit 2
  fi

  case "${BUMP}" in
    patch|minor|major) ;;
    *)
      usage
      printf 'invalid BUMP: %s\n' "${BUMP}" >&2
      exit 2
      ;;
  esac

  case "${DRY_RUN}" in
    true|false) ;;
    *)
      printf 'invalid DRY_RUN: %s (expected true or false)\n' "${DRY_RUN}" >&2
      exit 2
      ;;
  esac
}

latest_service_version() {
  local service="$1"

  git tag --list "deploy/${DEPLOY_ENV}/${service}/v[0-9]*.[0-9]*.[0-9]*" |
    sed -E 's#^deploy/[^/]+/[^/]+/v([0-9]+)\.([0-9]+)\.([0-9]+)$#\1.\2.\3#' |
    awk -F. 'NF == 3 { printf "%d.%d.%d\n", $1, $2, $3 }' |
    sort -t. -k1,1n -k2,2n -k3,3n |
    tail -n 1
}

bump_version() {
  local current="$1"
  local bump="$2"
  local major minor patch

  if [ -z "${current}" ]; then
    printf '%s\n' 'v0.1.0'
    return 0
  fi

  IFS=. read -r major minor patch <<EOF
${current}
EOF

  case "${bump}" in
    major)
      major=$((major + 1))
      minor=0
      patch=0
      ;;
    minor)
      minor=$((minor + 1))
      patch=0
      ;;
    patch)
      patch=$((patch + 1))
      ;;
  esac

  printf 'v%s.%s.%s\n' "${major}" "${minor}" "${patch}"
}

latest_group_baseline() {
  git for-each-ref \
    --merged HEAD \
    --sort=-creatordate \
    --format='%(refname:short)' \
    "refs/tags/deploy/${DEPLOY_ENV}/changed" \
    "refs/tags/deploy/${DEPLOY_ENV}/all" 2>/dev/null |
    head -n 1
}

write_all_services() {
  local service
  for service in "${SERVICES[@]}"; do
    printf '%s\n' "${service}"
  done
}

add_service_once() {
  local service="$1"
  local target_file="$2"

  if ! grep -qx "${service}" "${target_file}"; then
    printf '%s\n' "${service}" >> "${target_file}"
  fi
}

write_ordered_services() {
  local source_file="$1"
  local service

  for service in "${SERVICES[@]}"; do
    if grep -qx "${service}" "${source_file}"; then
      printf '%s\n' "${service}"
    fi
  done
}

select_changed_services() {
  local output_file="$1"
  local raw_services_file="$2"
  local changes_file="$3"
  local base_ref
  local path
  local common_changed=false

  base_ref="$(latest_group_baseline || true)"
  if [ -z "${base_ref}" ]; then
    write_all_services > "${output_file}"
    printf '%s\n' 'no previous changed/all deploy tag reachable from HEAD'
    return 0
  fi

  git diff --name-only "${base_ref}..HEAD" > "${changes_file}"

  while IFS= read -r path; do
    case "${path}" in
      services/auth-service/*|contracts/services/auth-service/*)
        add_service_once auth-service "${raw_services_file}"
        ;;
      services/concert-service/*|contracts/services/concert-service/*)
        add_service_once concert-service "${raw_services_file}"
        ;;
      services/reservation-service/*|contracts/services/reservation-service/*)
        add_service_once reservation-service "${raw_services_file}"
        ;;
      services/payment-service/*|contracts/services/payment-service/*)
        add_service_once payment-service "${raw_services_file}"
        ;;
      services/ticket-service/*|contracts/services/ticket-service/*)
        add_service_once ticket-service "${raw_services_file}"
        ;;
      services/notification-service/*|contracts/services/notification-service/*)
        add_service_once notification-service "${raw_services_file}"
        ;;
      packages/*|pyproject.toml|uv.lock|requirements-dev-overrides.txt|Taskfile.yml|.dockerignore|.github/workflows/image-build.yml|.github/workflows/image-publish.yml)
        common_changed=true
        ;;
    esac
  done < "${changes_file}"

  if [ "${common_changed}" = "true" ]; then
    write_all_services > "${output_file}"
    printf 'common image input changed since %s\n' "${base_ref}"
    return 0
  fi

  write_ordered_services "${raw_services_file}" > "${output_file}"
  if [ ! -s "${output_file}" ]; then
    printf 'no service image changes detected since %s\n' "${base_ref}" >&2
    printf 'Use SERVICE=<service> for an explicit single-service deploy or SERVICE=all for a forced full deploy.\n' >&2
    exit 1
  fi

  printf 'changed paths compared with %s\n' "${base_ref}"
}

next_group_tag() {
  local target="$1"
  local today
  local latest_sequence

  today="$(date +%Y.%m.%d)"
  latest_sequence="$(
    git tag --list "deploy/${DEPLOY_ENV}/${target}/${today}-*" |
      awk -F- '{ print $NF }' |
      awk '/^[0-9]+$/ { print $1 }' |
      sort -n |
      tail -n 1
  )"

  if [ -z "${latest_sequence}" ]; then
    latest_sequence=0
  fi

  printf 'deploy/%s/%s/%s-%s\n' "${DEPLOY_ENV}" "${target}" "${today}" "$((latest_sequence + 1))"
}

build_plan() {
  local target="$1"
  local tag_name="$2"
  local reason="$3"
  local services_file="$4"
  local plan_file="$5"
  local plan_services_file="$6"
  local source_sha
  local service
  local current
  local next
  local services_json

  source_sha="$(git rev-parse HEAD)"
  : > "${plan_services_file}"

  while IFS= read -r service; do
    current="$(latest_service_version "${service}")"
    next="$(bump_version "${current}" "${BUMP}")"
    jq -nc \
      --arg image "${service}" \
      --arg tag "${next}" \
      --arg previous_tag "${current}" \
      '{
        image: $image,
        tag: $tag
      } + if $previous_tag == "" then {} else {previous_tag: ("v" + $previous_tag)} end' >> "${plan_services_file}"
  done < "${services_file}"

  services_json="$(jq -s -c '.' "${plan_services_file}")"

  jq -n \
    --arg environment "${DEPLOY_ENV}" \
    --arg target "${target}" \
    --arg bump "${BUMP}" \
    --arg deploy_tag "${tag_name}" \
    --arg source_sha "${source_sha}" \
    --arg reason "${reason}" \
    --argjson services "${services_json}" \
    '{
      schema_version: 1,
      environment: $environment,
      target: $target,
      bump: $bump,
      deploy_tag: $deploy_tag,
      source_sha: $source_sha,
      reason: $reason,
      services: $services
    }' > "${plan_file}"
}

print_plan() {
  local plan_file="$1"

  jq -r '
    "deploy tag: \(.deploy_tag)",
    "environment: \(.environment)",
    "target: \(.target)",
    "bump: \(.bump)",
    "reason: \(.reason)",
    "services:",
    (.services[] | "- \(.image): \(.tag)" + (if .previous_tag then " (from \(.previous_tag))" else " (initial)" end))
  ' "${plan_file}"
}

create_and_push_tag() {
  local tag_name="$1"
  local plan_file="$2"

  if git rev-parse -q --verify "refs/tags/${tag_name}" >/dev/null; then
    printf 'tag already exists: %s\n' "${tag_name}" >&2
    exit 1
  fi

  git tag -a "${tag_name}" -F "${plan_file}"
  git push origin "refs/tags/${tag_name}"
}

main() {
  local tmp_dir
  local services_file
  local raw_services_file
  local changes_file
  local plan_services_file
  local plan_file
  local tag_name
  local reason

  require_cmd git
  require_cmd jq
  validate_inputs

  tmp_dir="$(mktemp -d)"
  trap "rm -rf '${tmp_dir}'" EXIT

  services_file="${tmp_dir}/services.txt"
  raw_services_file="${tmp_dir}/raw-services.txt"
  changes_file="${tmp_dir}/changes.txt"
  plan_services_file="${tmp_dir}/services.ndjson"
  plan_file="${tmp_dir}/deploy-plan.json"
  : > "${raw_services_file}"

  git fetch --tags --force

  if is_service "${SERVICE}"; then
    printf '%s\n' "${SERVICE}" > "${services_file}"
    tag_name="deploy/${DEPLOY_ENV}/${SERVICE}/$(bump_version "$(latest_service_version "${SERVICE}")" "${BUMP}")"
    reason="single service deploy"
  elif [ "${SERVICE}" = "changed" ]; then
    reason="$(select_changed_services "${services_file}" "${raw_services_file}" "${changes_file}")"
    tag_name="$(next_group_tag changed)"
  else
    write_all_services > "${services_file}"
    tag_name="$(next_group_tag all)"
    reason="forced full deploy"
  fi

  build_plan "${SERVICE}" "${tag_name}" "${reason}" "${services_file}" "${plan_file}" "${plan_services_file}"
  print_plan "${plan_file}"

  if [ "${DRY_RUN}" = "true" ]; then
    printf '%s\n' 'DRY_RUN=true: tag creation and push skipped.'
    return 0
  fi

  create_and_push_tag "${tag_name}" "${plan_file}"
  printf 'pushed deploy tag: %s\n' "${tag_name}"
}

main "$@"
