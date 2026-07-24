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

This repository does not contain a live attestation collector or fixture
provisioner. The runner therefore remains blocked with
`LIVE_FIXTURE_ATTESTATION_REQUIRED` until an independently reviewed,
read-only control-plane artifact proves the two credential bindings, the
dedicated 42-stock fixture, and zero active records. No collector or AWS
traffic is invented by this runner.

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
