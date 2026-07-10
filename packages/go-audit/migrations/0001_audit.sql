-- +goose Up
CREATE TABLE audit_outbox (
    id uuid PRIMARY KEY,
    event_name text NOT NULL,
    event_version integer NOT NULL CHECK (event_version > 0),
    occurred_at timestamptz NOT NULL,
    actor jsonb NOT NULL,
    resource jsonb NOT NULL,
    payload jsonb NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    idempotency_key text NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'delivered', 'dead')),
    attempt_count integer NOT NULL DEFAULT 0,
    available_at timestamptz NOT NULL DEFAULT now(),
    lease_owner text,
    lease_until timestamptz,
    last_error text,
    delivered_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (event_name, idempotency_key)
);

CREATE INDEX audit_outbox_claim_idx
    ON audit_outbox (status, available_at, occurred_at)
    WHERE status IN ('pending', 'processing');

CREATE TABLE audit_events (
    id uuid PRIMARY KEY,
    event_name text NOT NULL,
    event_version integer NOT NULL,
    occurred_at timestamptz NOT NULL,
    actor jsonb NOT NULL,
    resource jsonb NOT NULL,
    payload jsonb NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    idempotency_key text NOT NULL,
    archived_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_events_occurred_at_idx ON audit_events (occurred_at);
