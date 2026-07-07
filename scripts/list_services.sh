#!/usr/bin/env sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
REPO_ROOT=$(CDPATH= cd -- "${SCRIPT_DIR}/.." && pwd -P)

MODE=images
FORMAT=lines
COMMAND=list
CHANGED_FILES=
REQUESTED=
FORCE_ALL=false
COMMON_PATTERNS=

usage() {
  cat >&2 <<'EOF'
Usage:
  scripts/list_services.sh list --mode <images|tests|pyprojects> [--format <lines|shell|json>]
  scripts/list_services.sh select --mode <images|tests> [--changed-files <path>] [--requested <service|all>] [--all] [--common <path>] [--format <lines|shell|json>]

Service inventory:
  config/services.yml

Environment filters:
  SERVICE_INCLUDE="service-a service-b"
  SERVICE_EXCLUDE="service-c"
EOF
}

if [ "$#" -gt 0 ]; then
  COMMAND=$1
  shift
fi

while [ "$#" -gt 0 ]; do
  case "$1" in
    --mode)
      MODE=${2:-}
      shift 2
      ;;
    --format)
      FORMAT=${2:-}
      shift 2
      ;;
    --changed-files)
      CHANGED_FILES=${2:-}
      shift 2
      ;;
    --requested)
      REQUESTED=${2:-}
      shift 2
      ;;
    --all)
      FORCE_ALL=true
      shift
      ;;
    --common)
      COMMON_PATTERNS="${COMMON_PATTERNS}
${2:-}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage
      printf 'unknown argument: %s\n' "$1" >&2
      exit 2
      ;;
  esac
done

case "${MODE}" in
  images|tests|pyprojects) ;;
  *)
    usage
    printf 'unknown mode: %s\n' "${MODE}" >&2
    exit 2
    ;;
esac

case "${FORMAT}" in
  lines|shell|json) ;;
  *)
    usage
    printf 'unknown format: %s\n' "${FORMAT}" >&2
    exit 2
    ;;
esac

normalize_words() {
  printf '%s\n' "$1" | tr ',[:space:]' '\n' | sed '/^$/d'
}

read_services() {
  file=${SERVICES_CONFIG:-"${REPO_ROOT}/config/services.yml"}
  if [ ! -f "${file}" ]; then
    return 0
  fi

  section=${MODE}
  if [ "${MODE}" = "pyprojects" ]; then
    section=tests
  fi

  awk -v section="${section}" '
    function trim(value) {
      sub(/^[[:space:]]+/, "", value)
      sub(/[[:space:]]+$/, "", value)
      return value
    }
    /^[[:space:]]*#/ || /^[[:space:]]*$/ { next }
    /^[[:alnum:]_-]+:[[:space:]]*\[\][[:space:]]*$/ {
      current = ""
      next
    }
    /^[[:alnum:]_-]+:[[:space:]]*$/ {
      current = $0
      sub(/:.*/, "", current)
      next
    }
    current == section && /^[[:space:]]*-[[:space:]]*/ {
      item = $0
      sub(/^[[:space:]]*-[[:space:]]*/, "", item)
      sub(/[[:space:]]*#.*/, "", item)
      item = trim(item)
      gsub(/^["'\'']|["'\'']$/, "", item)
      if (item != "") {
        print item
      }
    }
  ' "${file}" | sort -u
}

service_exists() {
  candidate=$1
  services=$2
  printf '%s\n' "${services}" | grep -qx -- "${candidate}"
}

normalize_service() {
  raw=$1
  services=$2
  if service_exists "${raw}" "${services}"; then
    printf '%s\n' "${raw}"
    return 0
  fi
  if service_exists "${raw}-service" "${services}"; then
    printf '%s\n' "${raw}-service"
    return 0
  fi
  return 1
}

apply_filters() {
  services=$1
  include=${SERVICE_INCLUDE:-}
  exclude=${SERVICE_EXCLUDE:-}

  if [ -n "${include}" ]; then
    selected=
    missing=
    for item in $(normalize_words "${include}"); do
      if normalized=$(normalize_service "${item}" "${services}"); then
        selected="${selected}
${normalized}"
      else
        missing="${missing} ${item}"
      fi
    done
    if [ -n "${missing}" ]; then
      printf 'unknown SERVICE_INCLUDE values:%s\n' "${missing}" >&2
      exit 2
    fi
    services=$(printf '%s\n' "${selected}" | sed '/^$/d' | sort -u)
  fi

  if [ -n "${exclude}" ]; then
    for item in $(normalize_words "${exclude}"); do
      normalized=$(normalize_service "${item}" "${services}" || printf '%s\n' "${item}")
      services=$(printf '%s\n' "${services}" | grep -vx -- "${normalized}" || true)
    done
  fi

  printf '%s\n' "${services}" | sed '/^$/d'
}

format_services() {
  services=$1
  if [ "${MODE}" = "pyprojects" ]; then
    services=$(printf '%s\n' "${services}" | sed '/^$/d; s#^#services/#; s#$#/pyproject.toml#')
  fi

  case "${FORMAT}" in
    lines)
      printf '%s\n' "${services}" | sed '/^$/d'
      ;;
    shell)
      printf '%s\n' "${services}" | sed '/^$/d' | paste -sd ' ' -
      ;;
    json)
      if [ -z "$(printf '%s\n' "${services}" | sed '/^$/d')" ]; then
        printf '[]\n'
      else
        printf '%s\n' "${services}" |
          sed '/^$/d' |
          awk 'BEGIN { printf "[" } { gsub(/\\/,"\\\\"); gsub(/"/,"\\\""); printf "%s\"%s\"", sep, $0; sep="," } END { printf "]\n" }'
      fi
      ;;
  esac
}

path_matches() {
  pattern=$1
  path=$2
  [ "${path}" = "${pattern}" ] || case "${path}" in "${pattern}"/*) true ;; *) false ;; esac
}

select_changed_services() {
  services=$1
  file=$2
  selected=
  common_changed=false

  [ -f "${file}" ] || {
    printf '%s\n' "${services}"
    return 0
  }

  while IFS= read -r path; do
    [ -n "${path}" ] || continue
    for pattern in ${COMMON_PATTERNS}; do
      if path_matches "${pattern}" "${path}"; then
        common_changed=true
      fi
    done

    case "${path}" in
      services/*/*)
        candidate=${path#services/}
        candidate=${candidate%%/*}
        ;;
      contracts/services/*/*)
        candidate=${path#contracts/services/}
        candidate=${candidate%%/*}
        ;;
      *)
        candidate=
        ;;
    esac

    if [ -n "${candidate}" ] && service_exists "${candidate}" "${services}"; then
      selected="${selected}
${candidate}"
    fi
  done < "${file}"

  if [ "${common_changed}" = "true" ]; then
    printf '%s\n' "${services}"
  else
    printf '%s\n' "${selected}" | sed '/^$/d' | sort -u
  fi
}

services=$(read_services || true)
services=$(apply_filters "${services}")

case "${COMMAND}" in
  list)
    format_services "${services}"
    ;;
  select)
    if [ "${FORCE_ALL}" = "true" ] || [ "${REQUESTED}" = "all" ]; then
      selected=${services}
    elif [ -n "${REQUESTED}" ]; then
      if ! selected=$(normalize_service "${REQUESTED}" "${services}"); then
        printf 'unknown service for mode=%s: %s\n' "${MODE}" "${REQUESTED}" >&2
        exit 2
      fi
    else
      selected=$(select_changed_services "${services}" "${CHANGED_FILES}")
    fi
    format_services "${selected}"
    ;;
  *)
    usage
    printf 'unknown command: %s\n' "${COMMAND}" >&2
    exit 2
    ;;
esac
