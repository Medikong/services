#!/bin/sh

set -eu

readonly DEFAULT_SOURCE_ORIGIN='../archive/blueprint/50-service-design/A_300_auth/A_300_40-api/openapi'

script_dir=$(CDPATH= cd "$(dirname "$0")" && pwd)
auth_service_dir=$(CDPATH= cd "$script_dir/.." && pwd)
service_root=$(CDPATH= cd "$auth_service_dir/../.." && pwd)
snapshot_dir="$auth_service_dir/api/openapi"

fail() {
  printf '%s\n' "auth OpenAPI check: $*" >&2
  exit 1
}

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  shasum -a 256 "$1" | awk '{print $1}'
}

require_metadata_hash() {
  hash=$1
  grep -F "\"bundleSha256\": \"$hash\"" "$snapshot_dir/source.json" >/dev/null || \
    fail "source metadata does not contain the expected bundle hash for $2"
}

snapshot_only=false
case "${1:-}" in
  '')
    ;;
  --snapshot-only)
    snapshot_only=true
    ;;
  *)
    fail "usage: $0 [--snapshot-only]"
    ;;
esac

for required in openapi.bundle.yaml dev.openapi.bundle.yaml redocly.yaml source.json; do
  [ -f "$snapshot_dir/$required" ] || fail "snapshot file is missing: $snapshot_dir/$required"
done

production_bundle_sha=$(sha256_file "$snapshot_dir/openapi.bundle.yaml")
development_bundle_sha=$(sha256_file "$snapshot_dir/dev.openapi.bundle.yaml")
require_metadata_hash "$production_bundle_sha" openapi.bundle.yaml
require_metadata_hash "$development_bundle_sha" dev.openapi.bundle.yaml

if grep -F '/api/v1/dev/' "$snapshot_dir/openapi.bundle.yaml" >/dev/null; then
  fail 'production bundle contains a development route'
fi

if [ "$snapshot_only" = true ]; then
  printf '%s\n' 'auth OpenAPI bundle snapshot is internally consistent'
  exit 0
fi

if [ -n "${AUTH_OPENAPI_SOURCE_DIR:-}" ]; then
  source_dir=$AUTH_OPENAPI_SOURCE_DIR
  sync_source_override=$AUTH_OPENAPI_SOURCE_DIR
else
  source_dir="$service_root/$DEFAULT_SOURCE_ORIGIN"
  sync_source_override=
fi

[ -d "$source_dir" ] || fail "source directory is unavailable: $source_dir (use --snapshot-only in a service-only checkout or set AUTH_OPENAPI_SOURCE_DIR)"

temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/auth-openapi-check.XXXXXX")
cleanup() {
  rm -rf "$temporary_dir"
}
trap cleanup EXIT HUP INT TERM

if [ -n "$sync_source_override" ]; then
  AUTH_OPENAPI_SOURCE_DIR="$sync_source_override" \
    AUTH_OPENAPI_DEST_DIR="$temporary_dir" \
    "$script_dir/sync-openapi.sh" >/dev/null
else
  AUTH_OPENAPI_DEST_DIR="$temporary_dir" "$script_dir/sync-openapi.sh" >/dev/null
fi

for generated in openapi.bundle.yaml dev.openapi.bundle.yaml redocly.yaml source.json; do
  cmp -s "$snapshot_dir/$generated" "$temporary_dir/$generated" || \
    fail "snapshot is stale: run services/auth-service/scripts/sync-openapi.sh"
done

printf '%s\n' 'auth OpenAPI bundle snapshot matches the source of truth'
