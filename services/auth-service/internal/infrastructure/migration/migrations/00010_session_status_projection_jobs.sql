-- +goose Up
CREATE TABLE auth_session_status_projection_jobs (
    job_id UUID PRIMARY KEY,
    session_id UUID NOT NULL,
    user_id UUID NOT NULL,
    session_version BIGINT NOT NULL CHECK (session_version >= 0),
    target_status VARCHAR(24) NOT NULL CHECK (target_status IN ('revoked', 'reuse_detected')),
    valid_until TIMESTAMPTZ NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    delivery_status VARCHAR(16) NOT NULL DEFAULT 'pending'
        CHECK (delivery_status IN ('pending', 'processing', 'delivered')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_owner VARCHAR(128) NULL,
    lease_until TIMESTAMPTZ NULL,
    delivered_at TIMESTAMPTZ NULL,
    last_error_code VARCHAR(64) NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (session_id, session_version),
    CHECK (delivery_status <> 'processing' OR (lease_owner IS NOT NULL AND lease_until IS NOT NULL)),
    CHECK (delivery_status <> 'delivered' OR delivered_at IS NOT NULL)
);

CREATE INDEX idx_auth_session_status_projection_claim
    ON auth_session_status_projection_jobs (delivery_status, available_at, lease_until, created_at)
    WHERE delivery_status IN ('pending', 'processing');

-- Keep migration 00008's row-version and domain-event triggers intact. This
-- separate AFTER trigger reads their final row version and enqueues the sole
-- Redis projection job in the same transaction as the status update.
-- +goose StatementBegin
CREATE FUNCTION auth_enqueue_session_status_projection_job()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.session_status = 'active'
       AND NEW.session_status IN ('revoked', 'reuse_detected') THEN
        INSERT INTO auth_session_status_projection_jobs (
            job_id, session_id, user_id, session_version, target_status,
            valid_until, occurred_at, delivery_status, available_at
        ) VALUES (
            gen_random_uuid(), NEW.session_id, NEW.user_id, NEW.row_version,
            NEW.session_status, NEW.absolute_expires_at,
            CASE NEW.session_status
                WHEN 'reuse_detected' THEN COALESCE(NEW.reuse_detected_at, now())
                ELSE COALESCE(NEW.revoked_at, now())
            END,
            'pending', now()
        )
        ON CONFLICT (session_id, session_version) DO NOTHING;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER trg_auth_enqueue_session_status_projection_job
AFTER UPDATE OF session_status ON auth_sessions
FOR EACH ROW
EXECUTE FUNCTION auth_enqueue_session_status_projection_job();

-- Recover terminal changes already recorded by migration 00008, including
-- events an older status worker may have dead-lettered. Versioned Redis writes
-- make replaying an older terminal state safe.
INSERT INTO auth_session_status_projection_jobs (
    job_id, session_id, user_id, session_version, target_status,
    valid_until, occurred_at, delivery_status, available_at
)
SELECT
    gen_random_uuid(), session.session_id, session.user_id,
    event.aggregate_version, event.payload ->> 'status',
    session.absolute_expires_at, event.occurred_at, 'pending', now()
FROM auth_outbox_events AS event
JOIN auth_sessions AS session ON session.session_id = event.aggregate_id
WHERE event.aggregate_type = 'Session'
  AND event.event_type = 'Auth.SessionStatusCacheUpdated'
  AND event.payload ->> 'status' IN ('revoked', 'reuse_detected')
  AND session.absolute_expires_at > now()
ON CONFLICT (session_id, session_version) DO NOTHING;

-- Cover terminal sessions that predate 00008 or have no retained internal
-- outbox row. Expired sessions cannot pass the authorization decision.
INSERT INTO auth_session_status_projection_jobs (
    job_id, session_id, user_id, session_version, target_status,
    valid_until, occurred_at, delivery_status, available_at
)
SELECT
    gen_random_uuid(), session_id, user_id, row_version, session_status,
    absolute_expires_at,
    CASE session_status
        WHEN 'reuse_detected' THEN COALESCE(reuse_detected_at, updated_at)
        ELSE COALESCE(revoked_at, updated_at)
    END,
    'pending', now()
FROM auth_sessions
WHERE session_status IN ('revoked', 'reuse_detected')
  AND absolute_expires_at > now()
ON CONFLICT (session_id, session_version) DO NOTHING;

-- +goose Down
DROP TRIGGER IF EXISTS trg_auth_enqueue_session_status_projection_job ON auth_sessions;
DROP FUNCTION IF EXISTS auth_enqueue_session_status_projection_job();
DROP TABLE IF EXISTS auth_session_status_projection_jobs;
