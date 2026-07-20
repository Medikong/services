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

-- +goose StatementBegin
CREATE FUNCTION auth_enqueue_session_status_projection() RETURNS trigger AS $$
DECLARE
    transition_at TIMESTAMPTZ;
BEGIN
    IF OLD.session_status = 'active'
       AND NEW.session_status IN ('revoked', 'reuse_detected') THEN
        IF NEW.row_version <= OLD.row_version THEN
            NEW.row_version := OLD.row_version + 1;
        END IF;

        transition_at := CASE NEW.session_status
            WHEN 'reuse_detected' THEN COALESCE(NEW.reuse_detected_at, now())
            ELSE COALESCE(NEW.revoked_at, now())
        END;

        INSERT INTO auth_session_status_projection_jobs (
            job_id, session_id, user_id, session_version, target_status,
            valid_until, occurred_at, delivery_status, available_at
        ) VALUES (
            gen_random_uuid(), NEW.session_id, NEW.user_id, NEW.row_version,
            NEW.session_status, NEW.absolute_expires_at, transition_at, 'pending', now()
        )
        ON CONFLICT (session_id, session_version) DO NOTHING;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_auth_sessions_status_projection
    BEFORE UPDATE OF session_status ON auth_sessions
    FOR EACH ROW EXECUTE FUNCTION auth_enqueue_session_status_projection();

-- Queue terminal sessions that may still have an active Redis value when this
-- migration is deployed. Expired sessions cannot pass the status decision.
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
DROP TRIGGER IF EXISTS trg_auth_sessions_status_projection ON auth_sessions;
DROP FUNCTION IF EXISTS auth_enqueue_session_status_projection();
DROP TABLE IF EXISTS auth_session_status_projection_jobs;
