#!/bin/sh

set -eu

readonly REDOCLY_VERSION='2.38.0'
readonly DEFAULT_SOURCE_ORIGIN='../archive/blueprint/50-service-design/A_300_auth/A_300_40-api/openapi'

script_dir=$(CDPATH= cd "$(dirname "$0")" && pwd)
auth_service_dir=$(CDPATH= cd "$script_dir/.." && pwd)
service_root=$(CDPATH= cd "$auth_service_dir/../.." && pwd)
destination_dir=${AUTH_OPENAPI_DEST_DIR:-"$auth_service_dir/api/openapi"}

fail() {
  printf '%s\n' "auth OpenAPI sync: $*" >&2
  exit 1
}

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

if [ -n "${AUTH_OPENAPI_SOURCE_DIR:-}" ]; then
  source_dir=$AUTH_OPENAPI_SOURCE_DIR
else
  source_dir="$service_root/$DEFAULT_SOURCE_ORIGIN"
fi
source_origin=$DEFAULT_SOURCE_ORIGIN

[ -d "$source_dir" ] || fail "source directory is unavailable: $source_dir (set AUTH_OPENAPI_SOURCE_DIR to override)"
source_dir=$(CDPATH= cd "$source_dir" && pwd)

for required in openapi.yaml dev.openapi.yaml redocly.yaml; do
  [ -f "$source_dir/$required" ] || fail "source file is missing: $source_dir/$required"
done

temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/auth-openapi.XXXXXX")
cleanup() {
  rm -rf "$temporary_dir"
}
trap cleanup EXIT HUP INT TERM

npx --yes "@redocly/cli@$REDOCLY_VERSION" lint \
  --config "$source_dir/redocly.yaml" \
  "$source_dir/openapi.yaml" \
  "$source_dir/dev.openapi.yaml"

npx --yes "@redocly/cli@$REDOCLY_VERSION" bundle \
  --config "$source_dir/redocly.yaml" \
  "$source_dir/openapi.yaml" \
  --output "$temporary_dir/openapi.bundle.yaml"

npx --yes "@redocly/cli@$REDOCLY_VERSION" bundle \
  --config "$source_dir/redocly.yaml" \
  "$source_dir/dev.openapi.yaml" \
  --output "$temporary_dir/dev.openapi.bundle.yaml"

if grep -F '/api/v1/dev/' "$temporary_dir/openapi.bundle.yaml" >/dev/null; then
  fail 'production bundle contains a development route'
fi

production_source_sha=$(sha256_file "$source_dir/openapi.yaml")
development_source_sha=$(sha256_file "$source_dir/dev.openapi.yaml")
source_tree_sha=$(tree_sha256 "$source_dir")
production_bundle_sha=$(sha256_file "$temporary_dir/openapi.bundle.yaml")
development_bundle_sha=$(sha256_file "$temporary_dir/dev.openapi.bundle.yaml")

{
  printf '%s\n' '{'
  printf '%s\n' '  "schemaVersion": 1,'
  printf '%s\n' '  "generator": "@redocly/cli@2.38.0",'
  printf '  "sourceOrigin": "%s",\n' "$(json_escape "$source_origin")"
  printf '  "sourceTreeSha256": "%s",\n' "$source_tree_sha"
  printf '%s\n' '  "production": {'
  printf '%s\n' '    "entry": "openapi.yaml",'
  printf '    "sourceSha256": "%s",\n' "$production_source_sha"
  printf '%s\n' '    "bundle": "openapi.bundle.yaml",'
  printf '    "bundleSha256": "%s"\n' "$production_bundle_sha"
  printf '%s\n' '  },'
  printf '%s\n' '  "development": {'
  printf '%s\n' '    "entry": "dev.openapi.yaml",'
  printf '    "sourceSha256": "%s",\n' "$development_source_sha"
  printf '%s\n' '    "bundle": "dev.openapi.bundle.yaml",'
  printf '    "bundleSha256": "%s"\n' "$development_bundle_sha"
  printf '%s\n' '  }'
  printf '%s\n' '}'
} > "$temporary_dir/source.json"

cp "$source_dir/redocly.yaml" "$temporary_dir/redocly.yaml"
mkdir -p "$destination_dir"
mv "$temporary_dir/openapi.bundle.yaml" "$destination_dir/openapi.bundle.yaml"
mv "$temporary_dir/dev.openapi.bundle.yaml" "$destination_dir/dev.openapi.bundle.yaml"
mv "$temporary_dir/source.json" "$destination_dir/source.json"
mv "$temporary_dir/redocly.yaml" "$destination_dir/redocly.yaml"

printf 'auth OpenAPI bundles synchronized from %s\n' "$source_origin"
