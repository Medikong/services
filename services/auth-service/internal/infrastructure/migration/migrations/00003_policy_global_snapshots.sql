-- +goose Up
-- Policy rows are execution records and can have independent database IDs.
-- The operator API instead requires a single immutable version for the
-- complete policy surface, so it is modeled as its own aggregate.
CREATE TABLE auth_policy_global_snapshots (
    policy_snapshot_version BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    status VARCHAR(16) NOT NULL CHECK (status IN ('active', 'superseded')),
    document JSONB NOT NULL,
    activation_source VARCHAR(16) NOT NULL CHECK (activation_source IN ('bootstrap', 'operator')),
    activated_by_user_id UUID NULL,
    change_reason VARCHAR(500) NOT NULL,
    effective_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    superseded_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (
        (activation_source = 'bootstrap' AND activated_by_user_id IS NULL)
        OR (activation_source = 'operator' AND activated_by_user_id IS NOT NULL)
    )
);

CREATE UNIQUE INDEX uq_auth_policy_global_snapshots_active
    ON auth_policy_global_snapshots ((status = 'active'))
    WHERE status = 'active';

INSERT INTO auth_policy_global_snapshots (status, document, activation_source, change_reason)
SELECT 'active', jsonb_build_object(
    'loginLock', (SELECT rules FROM auth_policies WHERE policy_name = 'login_lock' AND status = 'active'),
    'sessionTtl', (SELECT rules FROM auth_policies WHERE policy_name = 'session_ttl' AND status = 'active'),
    'refreshRotation', (SELECT rules FROM auth_policies WHERE policy_name = 'refresh_rotation' AND status = 'active'),
    'verificationRules', (SELECT rules FROM auth_policies WHERE policy_name = 'verification_rules' AND status = 'active'),
    'sessionRevocationRules', (SELECT rules FROM auth_policies WHERE policy_name = 'session_revocation_rules' AND status = 'active')
), 'bootstrap', 'bootstrap';

-- +goose Down
DROP TABLE auth_policy_global_snapshots;
