# AWS Istio purchase runner preflight

`run_aws_purchase_auth.py` verifies the public JWT boundary that must pass before
an AWS purchase experiment may send purchase traffic. This preflight only calls
the JWKS route, login routes when needed, and `GET /v1/users/me/interests`. It never
sends an order, payment, inventory mutation, or trusted `X-User-*` identity
header.

## Runtime inputs

Configure exactly one public Istio ingress base URL with `--base-url` or
`AWS_PURCHASE_INGRESS_BASE_URL`. The trusted control plane must independently
inject `AWS_PURCHASE_EXPECTED_INGRESS_FINGERPRINT` as `sha256:` followed by the
64 lowercase hex characters for that approved normalized origin. The runner
never derives this approval from the requested URL. The control-plane endpoint
`http://10.20.10.4:32080` is a known environment-specific example, not a
default. Short, namespace-qualified, and `.svc` Kubernetes service DNS names
are rejected even if their fingerprint is supplied.

Use one approved credential mode:

- inject `SYNTHETIC_CUSTOMER_EMAIL` and `SYNTHETIC_CUSTOMER_PASSWORD` from
  Secret `synthetic/synthetic-traffic-credentials`; or
- inject an ephemeral access token as `AWS_PURCHASE_JWT`.

Do not place credentials on the command line, in fixture manifests, or in
artifacts. The runner emits only a base-URL fingerprint, run-derived request
identifiers, stage status, and reason codes.

```text
AWS_PURCHASE_EXPECTED_INGRESS_FINGERPRINT=sha256:<approved-64-hex-digest> \
uv run tests/e2e/scripts/run_aws_purchase_auth.py \
  --base-url http://10.20.10.4:32080 \
  --run-id aws-purchase-YYYYMMDDTHHMMSSZ-1234abcd \
  --json-output .omo/evidence/aws-purchase-auth.json \
  --junit-output .omo/evidence/aws-purchase-auth.junit.xml
```

Existing output paths are never overwritten. Exit `0` means the public JWT
boundary is verified, exit `2` is a security or authentication failure, exit
`3` is a blocked prerequisite, exit `4` is an output failure, and exit `130`
records interruption. `purchase_requests_sent` remains `0` in every result.

## Scenario 04 runner

`run_aws_purchase_scenarios.py` is the bounded Scenario 04 runner. It accepts
only `aws-dev`, requires a run-scoped redacted fixture manifest, and supports
`dry-run`, `preflight`, and `execute` modes. Preflight performs public login,
anonymous/authenticated notification checks, and catalog verification without
purchase writes. Execute additionally requires the exact run-scoped
`--write-opt-in` value and a `--live-fixture-attestation` artifact; order and
payment replays reuse deterministic idempotency keys, and reports contain only
fingerprints, stage status, counts, and reason codes.

```text
uv run tests/e2e/scripts/run_aws_purchase_scenarios.py \
  --environment aws-dev \
  --mode preflight \
  --scenario 04 \
  --base-url <approved-public-istio-origin> \
  --run-id aws-purchase-YYYYMMDDTHHMMSSZ-1234abcd \
  --fixture-manifest <redacted-fixture-manifest.json> \
  --json-output <scenario-04-report.json>
```

### Live fixture attestation

`collect_aws_purchase_live_fixture.py` creates the execute-mode attestation
only after all four read-only checks pass:

- public `GET /drops/{dropId}` reports the run-scoped drop `OPEN` and its
  expected product at 42 remaining units;
- `kubectl exec` runs `psql` with the database Pod's existing credentials and
  reads exactly one `inventory_items` row at total/reserved/sold `42/0/0`;
- the same Order database has zero `orders` rows for that exact drop/product
  pair;
- the configured Secret exposes all four customer email/password key names
  through a Go template. The collector never requests decoded values.

```text
uv run tests/e2e/scripts/collect_aws_purchase_live_fixture.py \
  --environment aws-dev \
  --fixture-manifest <redacted-fixture-manifest.json> \
  --catalog-base-url <approved-public-istio-origin> \
  --kube-context <approved-aws-dev-context> \
  --order-namespace <order-namespace> \
  --order-db-pod <postgres-pod> \
  --order-db-container <postgres-container> \
  --order-db-name <order-database> \
  --secret-namespace <synthetic-secret-namespace> \
  --secret-name <synthetic-secret-name> \
  --customer-a-email-key <customer-a-email-key-name> \
  --customer-a-password-key <customer-a-password-key-name> \
  --customer-b-email-key <customer-b-email-key-name> \
  --customer-b-password-key <customer-b-password-key-name> \
  --attestation-key-file <private-hmac-key-file> \
  --output <fresh-live-attestation.json>
```

The HMAC key file is read locally and never emitted. The collector writes a
fresh output atomically and refuses an existing output or lock. Its versioned
artifact includes a UTC `issued_at`, collector identity, and HMAC integrity.
Execute mode accepts it for at most five minutes and rejects forged, stale, or
caller-authored artifacts before HTTP traffic. The trusted control plane must
inject `AWS_PURCHASE_ATTESTATION_KEY_FINGERPRINT` from protected configuration;
the runner checks the supplied key's SHA-256 fingerprint against that value, so
a caller cannot authorize a new key by supplying a matching artifact and key
file. Pass the same protected key file to the runner:

For AWS-dev, the control plane resolves the private HMAC key from
`medikong/aws-dev/purchase-experiment/attestation-hmac-v1` and injects its
public fingerprint (`sha256:9382feff78085a9a1331ed08b9515db065fd7149b5f9f044f329af3637cb9b20`)
as that environment value. The expected fingerprint is never accepted as a
runner CLI argument.

```text
uv run tests/e2e/scripts/run_aws_purchase_scenarios.py \
  --environment aws-dev \
  --mode execute \
  --scenario 04 \
  --base-url <approved-public-istio-origin> \
  --run-id <same-run-id> \
  --fixture-manifest <same-redacted-fixture-manifest.json> \
  --live-fixture-attestation <fresh-live-attestation.json> \
  --attestation-key-file <private-hmac-key-file> \
  --write-opt-in aws-dev:<same-run-id>:ALLOW_PURCHASE_WRITES \
  --json-output <fresh-scenario-report.json>
```

The collector does not create fixtures, mutate inventory, place orders, send
payments, or provision Secrets.

## Current AWS-dev prerequisites

The current AWS-dev VirtualService routes `/auth*`, but the auth service exposes
`/.well-known/jwks.json`, `/api/v1/auth/intents`, and
`/api/v1/auth/signins/email`. Until those paths are exposed through the same
Istio ingress in
`e-gitops/platform/istio/aws-dev/routing/virtualservice.yaml`, the runner must
report `AUTH_ROUTE_UNRESOLVED`.

The control-plane runtime must also receive the two customer keys from
`synthetic/synthetic-traffic-credentials`, and the Todo2 run-scoped fixture
verifier must confirm its dedicated 42-stock fixture. Creating routes, Secrets,
or fixtures is intentionally outside this runner.
