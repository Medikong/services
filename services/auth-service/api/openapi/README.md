# Auth OpenAPI Bundle Snapshot

`archive/blueprint/50-service-design/A_300_auth/A_300_40-api/openapi/` is the source of truth. This directory contains only generated, self-contained bundles used by the service and CI.

- `openapi.bundle.yaml` is the production `API.A.300-01~29` bundle.
- `dev.openapi.bundle.yaml` is the development-only `API.A.300-30` and `API.A.300-34` bundle.
- `source.json` records the source origin and SHA-256 values for the source tree and both bundles.
- `redocly.yaml` is the source lint configuration copied with the bundle snapshot.

From the service repository root, run `task auth-openapi-sync` when the archive OpenAPI source changes. `task auth-openapi-check` re-bundles from the sibling archive checkout and rejects stale snapshots. In a service-only checkout, use `services/auth-service/scripts/check-openapi.sh --snapshot-only` to validate bundle integrity without the archive source tree.

The production bundle must never contain `/api/v1/dev/` routes.
