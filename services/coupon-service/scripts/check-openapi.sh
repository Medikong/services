#!/bin/sh

set -eu

script_dir=$(CDPATH= cd "$(dirname "$0")" && pwd)
coupon_service_dir=$(CDPATH= cd "$script_dir/.." && pwd)
snapshot_dir="$coupon_service_dir/api/openapi"

fail() {
  printf '%s\n' "coupon OpenAPI check: $*" >&2
  exit 1
}

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  shasum -a 256 "$1" | awk '{print $1}'
}

snapshot_only=false
case "${1:-}" in
  '') ;;
  --snapshot-only) snapshot_only=true ;;
  *) fail "usage: $0 [--snapshot-only]" ;;
esac

for required in openapi.bundle.yaml redocly.yaml source.json; do
  [ -f "$snapshot_dir/$required" ] || fail "snapshot file is missing: $snapshot_dir/$required"
done

bundle_sha=$(sha256_file "$snapshot_dir/openapi.bundle.yaml")
grep -F "\"bundleSha256\": \"$bundle_sha\"" "$snapshot_dir/source.json" >/dev/null || \
  fail 'source metadata does not contain the current bundle hash'

if [ "$snapshot_only" = true ]; then
  printf '%s\n' 'coupon OpenAPI bundle snapshot is internally consistent'
  exit 0
fi

temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/coupon-openapi-check.XXXXXX")
cleanup() {
  rm -rf "$temporary_dir"
}
trap cleanup EXIT HUP INT TERM

if [ -n "${COUPON_OPENAPI_SOURCE_DIR:-}" ]; then
  COUPON_OPENAPI_SOURCE_DIR="$COUPON_OPENAPI_SOURCE_DIR" \
    COUPON_OPENAPI_DEST_DIR="$temporary_dir" \
    "$script_dir/sync-openapi.sh" >/dev/null
else
  COUPON_OPENAPI_DEST_DIR="$temporary_dir" "$script_dir/sync-openapi.sh" >/dev/null
fi

for generated in openapi.bundle.yaml redocly.yaml source.json; do
  cmp -s "$snapshot_dir/$generated" "$temporary_dir/$generated" || \
    fail "snapshot is stale: run services/coupon-service/scripts/sync-openapi.sh"
done

printf '%s\n' 'coupon OpenAPI bundle snapshot matches the source of truth'
