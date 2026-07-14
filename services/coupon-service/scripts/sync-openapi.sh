#!/bin/sh

set -eu

readonly REDOCLY_VERSION='2.38.0'
readonly SOURCE_ORIGIN='../archive/blueprint/50-service-design/A_19_coupon/A_19_40-api/openapi'
readonly SOURCE_SUFFIX='blueprint/50-service-design/A_19_coupon/A_19_40-api/openapi'

script_dir=$(CDPATH= cd "$(dirname "$0")" && pwd)
coupon_service_dir=$(CDPATH= cd "$script_dir/.." && pwd)
service_root=$(CDPATH= cd "$coupon_service_dir/../.." && pwd)
destination_dir=${COUPON_OPENAPI_DEST_DIR:-"$coupon_service_dir/api/openapi"}

fail() {
  printf '%s\n' "coupon OpenAPI sync: $*" >&2
  exit 1
}

[ "$#" -eq 0 ] || fail "usage: $0"

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  shasum -a 256 "$1" | awk '{print $1}'
}

sha256_stdin() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum | awk '{print $1}'
    return
  fi
  shasum -a 256 | awk '{print $1}'
}

tree_sha256() {
  (
    cd "$1"
    find . -type f -name '*.yaml' -print | LC_ALL=C sort | while IFS= read -r file; do
      printf '%s  %s\n' "$(sha256_file "$file")" "$file"
    done
  ) | sha256_stdin
}

json_escape() {
  printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g'
}

discover_source_dir() {
  for candidate in \
    "$service_root/../archive/$SOURCE_SUFFIX" \
    "$service_root/../../archive/$SOURCE_SUFFIX"
  do
    if [ -d "$candidate" ]; then
      CDPATH= cd "$candidate" && pwd
      return
    fi
  done

  common_dir=$(git -C "$service_root" rev-parse --git-common-dir 2>/dev/null || true)
  if [ -n "$common_dir" ]; then
    case "$common_dir" in
      /*) common_abs=$common_dir ;;
      *) common_abs="$service_root/$common_dir" ;;
    esac
    checkout_root=$(dirname "$common_abs")
    candidate="$(dirname "$checkout_root")/archive/$SOURCE_SUFFIX"
    if [ -d "$candidate" ]; then
      CDPATH= cd "$candidate" && pwd
      return
    fi
  fi
  return 1
}

if [ -n "${COUPON_OPENAPI_SOURCE_DIR:-}" ]; then
  source_dir=$COUPON_OPENAPI_SOURCE_DIR
else
  source_dir=$(discover_source_dir) || fail "source directory is unavailable (set COUPON_OPENAPI_SOURCE_DIR to override)"
fi

[ -d "$source_dir" ] || fail "source directory is unavailable: $source_dir"
source_dir=$(CDPATH= cd "$source_dir" && pwd)
for required in openapi.yaml redocly.yaml; do
  [ -f "$source_dir/$required" ] || fail "source file is missing: $source_dir/$required"
done

temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/coupon-openapi.XXXXXX")
cleanup() {
  rm -rf "$temporary_dir"
}
trap cleanup EXIT HUP INT TERM

npx --yes "@redocly/cli@$REDOCLY_VERSION" lint \
  --config "$source_dir/redocly.yaml" \
  "$source_dir/openapi.yaml"

npx --yes "@redocly/cli@$REDOCLY_VERSION" bundle \
  --config "$source_dir/redocly.yaml" \
  "$source_dir/openapi.yaml" \
  --output "$temporary_dir/openapi.bundle.yaml"

source_sha=$(sha256_file "$source_dir/openapi.yaml")
source_tree_sha=$(tree_sha256 "$source_dir")
bundle_sha=$(sha256_file "$temporary_dir/openapi.bundle.yaml")
source_commit=$(git -C "$source_dir" rev-parse HEAD 2>/dev/null) || fail "source directory is not inside the archive Git checkout"

{
  printf '%s\n' '{'
  printf '%s\n' '  "schemaVersion": 1,'
  printf '%s\n' '  "generator": "@redocly/cli@2.38.0",'
  printf '  "sourceOrigin": "%s",\n' "$(json_escape "$SOURCE_ORIGIN")"
	printf '  "sourceCommit": "%s",\n' "$source_commit"
  printf '  "sourceTreeSha256": "%s",\n' "$source_tree_sha"
  printf '%s\n' '  "production": {'
  printf '%s\n' '    "entry": "openapi.yaml",'
  printf '    "sourceSha256": "%s",\n' "$source_sha"
  printf '%s\n' '    "bundle": "openapi.bundle.yaml",'
  printf '    "bundleSha256": "%s"\n' "$bundle_sha"
  printf '%s\n' '  }'
  printf '%s\n' '}'
} > "$temporary_dir/source.json"

cp "$source_dir/redocly.yaml" "$temporary_dir/redocly.yaml"
mkdir -p "$destination_dir"
mv "$temporary_dir/openapi.bundle.yaml" "$destination_dir/openapi.bundle.yaml"
mv "$temporary_dir/source.json" "$destination_dir/source.json"
mv "$temporary_dir/redocly.yaml" "$destination_dir/redocly.yaml"

printf 'coupon OpenAPI bundle synchronized from %s\n' "$SOURCE_ORIGIN"
