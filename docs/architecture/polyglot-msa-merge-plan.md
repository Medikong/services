# Polyglot MSA merge plan

## Current branches

Local work on `feature/python-order-payment-flows` contains Python FastAPI
services for the purchase flow:

- `catalog-service`
- `order-service`
- `payment-service`
- `notification-service`

Remote `origin/main` contains Go services and Go common packages:

- `auth-service`
- `user-service`
- `coupon-service`
- `backoffice-service`
- `packages/go-platform`
- `packages/go-authz`
- `packages/go-contracts`

These branches are not complete duplicates. They cover different business
domains, but they overlap in service conventions, test entrypoints, auth
contracts, and DropMong E2E flow ownership.

## Merge principle

Use a polyglot MSA structure instead of forcing every service into one
language.

- Keep Go for auth, user, coupon, and backoffice domains.
- Keep Python FastAPI for the current order, payment, catalog, and notification
  purchase-flow prototype unless a measured performance bottleneck appears.
- Share contracts at HTTP, Kafka, header, error, health, metrics, and Docker
  boundaries.
- Avoid cross-language imports. Services communicate through HTTP, Kafka, and
  documented contracts only.

## Target service map

| Service | Language | Current source | Responsibility |
| --- | --- | --- | --- |
| `auth-service` | Go | `origin/main` | Principal, token, and auth flow |
| `user-service` | Go | `origin/main` | User profile lifecycle |
| `coupon-service` | Go | `origin/main` | Coupon policy and issue flow |
| `backoffice-service` | Go | `origin/main` | Operator drop/product/coupon setup |
| `catalog-service` | Python | local branch | Public drop/catalog read model |
| `order-service` | Python | local branch | Order creation, idempotency, stock reservation |
| `payment-service` | Python | local branch | Mock approval/failure and payment events |
| `notification-service` | Python | local branch | Notification persistence and listing |

## Contract decisions to settle before merge

1. Auth header contract

   Remote Go E2E uses an auth-service issued principal header. Local Python
   services currently accept `X-User-Id` and `X-User-Role`. Merge should define
   whether Python services accept the same principal header directly or whether
   the gateway translates JWT claims into the existing headers.

2. Catalog and backoffice boundary

   `backoffice-service` owns operator-managed drop/product setup. The Python
   `catalog-service` should either remain a public read model or be merged into
   the Go backoffice/read side later. Do not duplicate write ownership.

3. Coupon and order boundary

   `coupon-service` should stay responsible for coupon issue/admission. The
   order flow should call or consume coupon decisions through a contract, not
   reimplement coupon rules inside `order-service`.

4. E2E runner shape

   Remote uses `tests/e2e/scripts/dropmong_scenarios.py`; local purchase flow
   uses Newman collections. Keep both temporarily during merge, then choose one
   canonical runner or make the Python script drive the same 04/05/06 purchase
   scenarios.

## Conflict hot spots

- `tests/README.md`
- `tests/Taskfile.yml`
- `tests/e2e/docker-compose.yml`
- `tests/e2e/postgres-init/01-create-databases.sql`
- `config/services.yml`
- `contracts/common/components.yaml`
- `contracts/jwt-conventions.md`
- `AGENTS.md`

Resolve these by preserving remote Go service rules and adding Python purchase
services as additional services, not by replacing one side wholesale.

## Recommended merge order

1. Keep this local branch as a protected checkpoint.
2. Update a clean `main` from `origin/main`.
3. Create a merge branch from the updated `main`.
4. Bring in local Python service directories first.
5. Merge shared contracts and E2E files file-by-file.
6. Reconcile auth headers and service ports.
7. Run unit tests for Go and Python services that are present.
8. Run E2E for auth/user/coupon/backoffice and purchase flows.

## Verified local purchase flows

- `payment-service`: `19 passed`
- `order-service`: `17 passed`
- `05-payment-failure-flow`: Newman E2E passed against Docker/Postgres/Kafka
- `06-sold-out-concurrency-flow`: Newman E2E passed against Docker/Postgres/Kafka
