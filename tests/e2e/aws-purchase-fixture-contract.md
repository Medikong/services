# AWS purchase experiment fixture contract

This contract is separate from the Docker/Newman internal regression fixtures.
The existing 04/05/06 collections remain unchanged and are not valid evidence
for the AWS Istio experiment.

## Safety boundary

The experiment owns one synthetic run namespace and nothing else:

- exactly two distinct customer identity references;
- one run-scoped drop reference and one distinct product reference;
- exactly 42 units of initial stock;
- zero active order, payment, or notification records for the run;
- no database reset, shared seed mutation, automatic compensation, or immediate
  cleanup.

Credentials remain in an environment-managed Kubernetes Secret. The input
contains only opaque references and a presence attestation; it never contains
an email, password, token, cookie, authorization value, or raw customer
identifier. Opaque references must match `opaque-[a-z0-9][a-z0-9-]{0,62}` and
must not encode a username, email address, or other PII.

The immutable run identifier matches
`aws-purchase-YYYYMMDDTHHMMSSZ-<8 lowercase hex characters>`. The fixture
namespace must equal that run identifier. Every later request ID, idempotency
key, structured log, and report must derive from the same identifier; a new
identifier is required for every attempted experiment.

## Redacted input schema

The verifier accepts this shape. Angle-bracketed entries are rules, not fixture
values.

```json
{
  "schema_version": 1,
  "run_id": "<aws-purchase-YYYYMMDDTHHMMSSZ-8hex>",
  "users": [
    {
      "subject_ref": "<opaque-reference>",
      "credential_ref": "<opaque-reference>",
      "credential_status": "present",
      "role": "customer"
    },
    {
      "subject_ref": "<different-opaque-reference>",
      "credential_ref": "<different-opaque-reference>",
      "credential_status": "present",
      "role": "customer"
    }
  ],
  "fixture": {
    "namespace": "<same-run-id>",
    "drop_ref": "<opaque-reference>",
    "product_ref": "<different-opaque-reference>",
    "dedicated": true,
    "stock": 42
  },
  "active_records": [],
  "retention": {
    "days": 30,
    "cleanup": "retention_only",
    "automatic_compensation": false
  }
}
```

Unknown fields are rejected so secret values cannot be smuggled into a
diagnostic artifact. Output contains only the validated run ID, counts,
allowlisted state, and short fingerprints of opaque references.

## State and retention

The preflight starts only when there are no active records for the run ID. The
expected terminal states are:

| Scenario | Order | Payment | Notification |
| --- | --- | --- | --- |
| successful purchase | `CONFIRMED` | `APPROVED` | exactly one `ORDER_CONFIRMED` |
| declined payment | `PAYMENT_FAILED` | `FAILED` with `card_declined` | no false confirmation |
| sold-out/release | only accepted reservations persist | one selected payment fails | later scenario assertions decide the terminal notification state |

Successful and failed synthetic business records remain available for diagnosis
and are removed only by the reviewed 30-day retention process. The verifier
does not delete records or reset a database.

The state directory is durable run history. Every valid preflight attempt,
including one blocked for unavailable live provisioning, creates one claim file
named by a run-ID fingerprint. Exclusive creation makes reuse fail closed.
Output is written through a same-directory temporary file and atomic replace; a
stale output lock is treated as an interrupted run and blocks a new artifact
instead of overwriting evidence.

## Invocation and verdicts

```text
uv run tests/e2e/scripts/verify_aws_purchase_fixture.py \
  --input <redacted-input.json> \
  --output <immutable-result.json> \
  --state-dir <durable-private-run-state>
```

The default invocation exits nonzero with
`LIVE_PROVISIONING_UNAVAILABLE`, sets `api_traffic_allowed` to `false`, and
must stop the caller before any purchase request.

`--contract-only` is limited to local schema/manual QA. A valid contract exits
zero as `LOCAL_CONTRACT_VERIFIED`, but its artifact still says runtime
provisioning is `BLOCKED` and `api_traffic_allowed` is `false`. It is never a
live-traffic authorization.

Refusal codes include:

- `USER_COUNT_INVALID`, `CREDENTIALS_MISSING`, and `DUPLICATE_USERS`;
- `FIXTURE_SCOPE_INVALID` and `FIXTURE_STOCK_INVALID`;
- `ACTIVE_RECORDS_PRESENT` and `RUN_ID_REUSED`;
- `MANIFEST_MISSING`, `MANIFEST_INVALID`, and `RETENTION_POLICY_INVALID`.

Every normal refusal writes the same redacted JSON artifact before returning
exit code 2. Operational blocks use exit code 4. No network client or
provisioning call exists in this verifier.

## Remaining live prerequisite

Runtime provisioning is intentionally `BLOCKED`. The services repository does
not contain an approved, read-only AWS-dev collector that can produce all of
these attestations without exposing values:

1. both customer credential entries exist in the Kubernetes Secret;
2. the run-scoped drop and product exist with exactly 42 units;
3. the run ID has no active order, payment, or notification records.

If the fixture is absent, an independently reviewed provisioning mechanism is
also required. This task does not invent that mechanism or send mutating
traffic. The purchase runner must remain blocked until those inputs exist and
are wired through a read-only live verifier.
