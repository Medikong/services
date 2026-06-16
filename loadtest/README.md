# Local k6 Loadtest Reports

`service` repo owns the executable loadtest scenarios. Local report artifacts are written under
`loadtest/reports/local/{run_id}/`; that directory is ignored by git so the same shape can later be
uploaded to an artifact store such as S3.

Run the read API baseline against a local gateway or service URL:

```bash
task loadtest LOADTEST_BASE_URL=http://localhost LOADTEST_VUS=5 LOADTEST_DURATION=1m
```

Each run writes:

- `metadata.json`
- `summary.json`
- `report.html`
- `report.md`

`loadtest/reports/local/latest` points to the most recent run. `run_id` is built from UTC timestamp,
scenario, and the short git SHA. Dynamic run IDs stay in artifacts and logs, not Prometheus labels.

Use the report-only smoke path to verify local artifact generation without a running service:

```bash
task loadtest-smoke
```

S3 upload and long-term AWS retention are intentionally out of scope for this step.
