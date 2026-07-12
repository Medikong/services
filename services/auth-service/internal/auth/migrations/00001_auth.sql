-- +goose Up

CREATE TABLE auth_policies (
    policy_version BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    policy_name VARCHAR(64) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'superseded')),
    rules JSONB NOT NULL DEFAULT '{}'::jsonb,
    login_failure_threshold SMALLINT NOT NULL DEFAULT 5
        CHECK (login_failure_threshold BETWEEN 1 AND 20),
    login_failure_window_seconds INTEGER NOT NULL DEFAULT 900
        CHECK (login_failure_window_seconds BETWEEN 1 AND 86400),
    lock_duration_seconds INTEGER NOT NULL DEFAULT 900
        CHECK (lock_duration_seconds BETWEEN 1 AND 604800),
    reset_failure_on_success BOOLEAN NOT NULL DEFAULT TRUE,
    web_idle_ttl_seconds INTEGER NOT NULL DEFAULT 43200
        CHECK (web_idle_ttl_seconds BETWEEN 1 AND 604800),
    web_absolute_ttl_seconds INTEGER NOT NULL DEFAULT 1209600
        CHECK (web_absolute_ttl_seconds BETWEEN web_idle_ttl_seconds AND 31536000),
    access_ttl_seconds INTEGER NOT NULL DEFAULT 900
        CHECK (access_ttl_seconds BETWEEN 1 AND 86400),
    refresh_ttl_seconds INTEGER NOT NULL DEFAULT 1209600
        CHECK (refresh_ttl_seconds > access_ttl_seconds AND refresh_ttl_seconds <= 31536000),
    remember_me_ttl_seconds INTEGER NOT NULL DEFAULT 2592000
        CHECK (remember_me_ttl_seconds >= refresh_ttl_seconds AND remember_me_ttl_seconds <= 31536000),
    internal_context_ttl_seconds INTEGER NOT NULL DEFAULT 900
        CHECK (internal_context_ttl_seconds BETWEEN 1 AND 86400),
    refresh_rotation_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    refresh_reuse_action VARCHAR(32) NOT NULL DEFAULT 'revoke_family_and_session'
        CHECK (refresh_reuse_action IN ('revoke_family_and_session')),
    activation_source VARCHAR(16) NOT NULL DEFAULT 'bootstrap'
        CHECK (activation_source IN ('bootstrap', 'operator')),
    activated_by_user_id UUID NULL,
    change_reason VARCHAR(500) NOT NULL DEFAULT 'bootstrap',
    effective_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    superseded_at TIMESTAMPTZ NULL,
    row_version BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (policy_name, policy_version),
    CHECK (
        (activation_source = 'bootstrap' AND activated_by_user_id IS NULL)
        OR (activation_source = 'operator' AND activated_by_user_id IS NOT NULL)
    )
);

CREATE UNIQUE INDEX uq_auth_policies_active
    ON auth_policies (policy_name)
    WHERE status = 'active';

CREATE TABLE auth_verification_policy_rules (
    policy_version BIGINT NOT NULL REFERENCES auth_policies (policy_version),
    purpose VARCHAR(32) NOT NULL,
    channel VARCHAR(24) NOT NULL,
    ttl_seconds INTEGER NOT NULL CHECK (ttl_seconds BETWEEN 1 AND 86400),
    resend_interval_seconds INTEGER NOT NULL CHECK (resend_interval_seconds BETWEEN 1 AND 86400),
    max_attempts SMALLINT NOT NULL CHECK (max_attempts BETWEEN 1 AND 20),
    max_sends SMALLINT NOT NULL CHECK (max_sends BETWEEN 1 AND 20),
    PRIMARY KEY (policy_version, purpose, channel),
    CHECK (purpose IN ('signup_email', 'signup_phone', 'phone_signin', 'password_reset', 'phone_change', 'identity_link')),
    CHECK (channel IN ('email_code', 'sms_code'))
);

CREATE TABLE auth_session_revocation_policy_rules (
    policy_version BIGINT NOT NULL REFERENCES auth_policies (policy_version),
    trigger VARCHAR(32) NOT NULL,
    scope VARCHAR(32) NOT NULL
        CHECK (scope IN ('current_session', 'identity_sessions', 'user_sessions', 'refresh_family')),
    PRIMARY KEY (policy_version, trigger, scope)
);

INSERT INTO auth_policies (policy_name, rules, change_reason)
VALUES
    ('login_lock', '{"threshold":5,"windowSeconds":900,"lockDurationSeconds":900}'::jsonb, 'bootstrap'),
    ('session_ttl', '{"webIdleSeconds":43200,"webAbsoluteSeconds":1209600,"accessSeconds":900}'::jsonb, 'bootstrap'),
    ('refresh_rotation', '{"enabled":true,"refreshSeconds":1209600,"rememberMeSeconds":2592000,"reuseAction":"revoke_family_and_session"}'::jsonb, 'bootstrap'),
    ('verification_rules', '{"emailCode":true,"smsCode":true}'::jsonb, 'bootstrap'),
    ('session_revocation_rules', '{"passwordReset":["user_sessions","refresh_family"]}'::jsonb, 'bootstrap');

CREATE TABLE auth_identities (
    identity_id UUID PRIMARY KEY,
    identity_type VARCHAR(32) NOT NULL
        CHECK (identity_type IN ('email', 'phone', 'provider_subject', 'passkey')),
    identity_namespace VARCHAR(64) NOT NULL DEFAULT 'default',
    normalized_value TEXT NOT NULL,
    value_ciphertext BYTEA NULL,
    value_key_id VARCHAR(128) NULL,
    value_lookup_hash BYTEA NULL CHECK (value_lookup_hash IS NULL OR octet_length(value_lookup_hash) = 32),
    lookup_key_version SMALLINT NULL,
    masked_value VARCHAR(320) NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'verified', 'active', 'locked', 'password_reset_required', 'revoked', 'superseded', 'expired', 'failed')),
    verification_status VARCHAR(24) NOT NULL DEFAULT 'pending'
        CHECK (verification_status IN ('pending', 'verified', 'expired', 'failed')),
    credential_status VARCHAR(32) NOT NULL DEFAULT 'active'
        CHECK (credential_status IN ('active', 'locked', 'password_reset_required', 'revoked', 'superseded')),
    owner_user_id UUID NULL,
    failure_count INTEGER NOT NULL DEFAULT 0 CHECK (failure_count >= 0),
    failure_window_started_at TIMESTAMPTZ NULL,
    lock_until TIMESTAMPTZ NULL,
    lock_policy_version BIGINT NULL REFERENCES auth_policies (policy_version),
    password_reset_required_at TIMESTAMPTZ NULL,
    password_reset_reason VARCHAR(64) NULL,
    verified_at TIMESTAMPTZ NULL,
    superseded_by_identity_id UUID NULL REFERENCES auth_identities (identity_id),
    row_version BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (identity_id, identity_type),
    UNIQUE (identity_type, identity_namespace, normalized_value),
    CHECK (superseded_by_identity_id IS NULL OR superseded_by_identity_id <> identity_id),
    CHECK (failure_count = 0 OR failure_window_started_at IS NOT NULL),
    CHECK (credential_status <> 'locked' OR (lock_until IS NOT NULL AND lock_policy_version IS NOT NULL)),
    CHECK (credential_status <> 'password_reset_required' OR (password_reset_required_at IS NOT NULL AND password_reset_reason IS NOT NULL)),
    CHECK (credential_status <> 'superseded' OR superseded_by_identity_id IS NOT NULL),
    CHECK (verification_status <> 'verified' OR verified_at IS NOT NULL)
);

CREATE INDEX idx_auth_identities_status_lock
    ON auth_identities (credential_status, lock_until);

CREATE TABLE auth_password_credentials (
    password_credential_id UUID PRIMARY KEY,
    identity_id UUID NOT NULL REFERENCES auth_identities (identity_id),
    password_hash TEXT NULL,
    password_status VARCHAR(16) NOT NULL DEFAULT 'active'
        CHECK (password_status IN ('active', 'replaced', 'revoked')),
    hash_algorithm VARCHAR(24) NOT NULL DEFAULT 'argon2id',
    hash_parameters JSONB NOT NULL DEFAULT '{}'::jsonb,
    credential_version INTEGER NOT NULL DEFAULT 1 CHECK (credential_version > 0),
    replaced_at TIMESTAMPTZ NULL,
    row_version BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (password_status <> 'active' OR password_hash IS NOT NULL)
);

CREATE UNIQUE INDEX uq_auth_password_credentials_active
    ON auth_password_credentials (identity_id)
    WHERE password_status = 'active';

CREATE TABLE auth_action_intent_payloads (
    action_payload_id UUID PRIMARY KEY,
    intent_id UUID NULL,
    action_name VARCHAR(64) NOT NULL,
    schema_version SMALLINT NOT NULL DEFAULT 1,
    payload_ciphertext BYTEA NULL,
    payload_key_id VARCHAR(128) NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    delivered_at TIMESTAMPTZ NULL,
    destroyed_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE auth_authentication_intents (
    intent_id UUID PRIMARY KEY,
    client_channel VARCHAR(16) NOT NULL CHECK (client_channel IN ('web', 'mobile')),
    return_path VARCHAR(1024) NOT NULL DEFAULT '/' CHECK (return_path ~ '^/[A-Za-z0-9/_-]*$'),
    intent_type VARCHAR(64) NOT NULL DEFAULT 'authenticate',
    action_context JSONB NULL,
    owner_proof_hash BYTEA NOT NULL UNIQUE CHECK (octet_length(owner_proof_hash) = 32),
    owner_proof_key_version SMALLINT NOT NULL DEFAULT 1,
    csrf_secret_hash BYTEA NULL CHECK (csrf_secret_hash IS NULL OR octet_length(csrf_secret_hash) = 32),
    csrf_key_version SMALLINT NULL,
    remember_me BOOLEAN NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'consumed', 'expired')),
    action_payload_id UUID NULL UNIQUE REFERENCES auth_action_intent_payloads (action_payload_id),
    consumed_by_session_id UUID NULL,
    consumption_reason VARCHAR(32) NULL CHECK (consumption_reason IS NULL OR consumption_reason IN ('session_issued', 'password_reset_completed', 'cancelled')),
    expires_at TIMESTAMPTZ NOT NULL,
    delivery_recovery_expires_at TIMESTAMPTZ NULL,
    consumed_at TIMESTAMPTZ NULL,
    row_version BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (status <> 'consumed' OR (consumed_at IS NOT NULL AND consumption_reason IS NOT NULL)),
    CHECK (client_channel <> 'web' OR (csrf_secret_hash IS NOT NULL AND csrf_key_version IS NOT NULL))
);

ALTER TABLE auth_action_intent_payloads
    ADD CONSTRAINT fk_auth_action_payloads_intent
    FOREIGN KEY (intent_id) REFERENCES auth_authentication_intents (intent_id);

CREATE UNIQUE INDEX uq_auth_action_payloads_intent
    ON auth_action_intent_payloads (intent_id)
    WHERE intent_id IS NOT NULL;

CREATE INDEX idx_auth_intents_status_expiry
    ON auth_authentication_intents (status, expires_at);

CREATE TABLE auth_registrations (
    registration_id UUID PRIMARY KEY,
    intent_id UUID NOT NULL REFERENCES auth_authentication_intents (intent_id),
    email_identity_id UUID NOT NULL REFERENCES auth_identities (identity_id),
    phone_identity_id UUID NOT NULL REFERENCES auth_identities (identity_id),
    email_challenge_id UUID NULL,
    phone_challenge_id UUID NULL,
    profile_request_id TEXT NOT NULL,
    agreement_receipt_id TEXT NOT NULL,
    remember_me BOOLEAN NOT NULL DEFAULT FALSE,
    client_channel VARCHAR(16) NOT NULL DEFAULT 'web' CHECK (client_channel IN ('web', 'mobile')),
    status VARCHAR(32) NOT NULL DEFAULT 'pending_verification'
        CHECK (status IN ('pending_verification', 'awaiting_user_link', 'linked', 'issuing_session', 'completed', 'failed', 'expired')),
    verified_methods TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    status_token_hash BYTEA NOT NULL UNIQUE CHECK (octet_length(status_token_hash) = 32),
    status_token_key_version SMALLINT NOT NULL DEFAULT 1,
    status_token_expires_at TIMESTAMPTZ NOT NULL,
    verification_binding_id UUID NULL UNIQUE,
    verification_registration_version BIGINT NULL,
    verification_snapshot_hash BYTEA NULL CHECK (verification_snapshot_hash IS NULL OR octet_length(verification_snapshot_hash) = 32),
    verification_completed_event_id UUID NULL UNIQUE,
    link_request_id UUID NULL UNIQUE,
    completion_idempotency_record_id UUID NULL UNIQUE,
    user_id UUID NULL,
    linked_at TIMESTAMPTZ NULL,
    session_id UUID NULL UNIQUE,
    session_policy_version BIGINT NULL REFERENCES auth_policies (policy_version),
    link_accept_until TIMESTAMPTZ NULL,
    session_issue_until TIMESTAMPTZ NULL,
    failure_code VARCHAR(64) NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ NULL,
    row_version BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (email_identity_id <> phone_identity_id),
    CHECK (array_length(verified_methods, 1) IS NULL OR verified_methods <@ ARRAY['email', 'phone']::TEXT[])
);

CREATE UNIQUE INDEX uq_auth_registrations_email_active
    ON auth_registrations (email_identity_id)
    WHERE status IN ('pending_verification', 'awaiting_user_link', 'linked', 'issuing_session');

CREATE UNIQUE INDEX uq_auth_registrations_phone_active
    ON auth_registrations (phone_identity_id)
    WHERE status IN ('pending_verification', 'awaiting_user_link', 'linked', 'issuing_session');

CREATE INDEX idx_auth_registrations_status_updated
    ON auth_registrations (status, updated_at);

CREATE INDEX idx_auth_registrations_verification_deadline
    ON auth_registrations (expires_at, registration_id)
    WHERE status = 'pending_verification';

CREATE INDEX idx_auth_registrations_link_deadline
    ON auth_registrations (link_accept_until, registration_id)
    WHERE status = 'awaiting_user_link';

CREATE INDEX idx_auth_registrations_session_deadline
    ON auth_registrations (session_issue_until, registration_id)
    WHERE status IN ('linked', 'issuing_session');

CREATE TABLE auth_challenges (
    challenge_id UUID PRIMARY KEY,
    subject_type VARCHAR(32) NOT NULL
        CHECK (subject_type IN ('registration', 'password_reset', 'identity_link', 'phone_signin', 'phone_change')),
    subject_id UUID NOT NULL,
    purpose VARCHAR(32) NOT NULL DEFAULT 'signup_email'
        CHECK (purpose IN ('signup_email', 'signup_phone', 'phone_signin', 'password_reset', 'phone_change', 'identity_link')),
    method VARCHAR(24) NOT NULL CHECK (method IN ('email', 'phone')),
    channel VARCHAR(24) NOT NULL DEFAULT 'email_code' CHECK (channel IN ('email_code', 'sms_code')),
    destination TEXT NOT NULL,
    destination_lookup_hash BYTEA NULL CHECK (destination_lookup_hash IS NULL OR octet_length(destination_lookup_hash) = 32),
    identity_id UUID NULL REFERENCES auth_identities (identity_id),
    code_hash BYTEA NOT NULL CHECK (octet_length(code_hash) = 32),
    verifier_key_version SMALLINT NOT NULL DEFAULT 1,
    status VARCHAR(16) NOT NULL DEFAULT 'issued' CHECK (status IN ('issued', 'verified', 'failed', 'expired', 'revoked')),
    attempt_count SMALLINT NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    max_attempts SMALLINT NOT NULL DEFAULT 5 CHECK (max_attempts BETWEEN 1 AND 20),
    send_count SMALLINT NOT NULL DEFAULT 1 CHECK (send_count >= 0),
    max_sends SMALLINT NOT NULL DEFAULT 5 CHECK (max_sends BETWEEN 1 AND 20),
    next_send_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    policy_version BIGINT NULL REFERENCES auth_policies (policy_version),
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ NULL,
    verified_at TIMESTAMPTZ NULL,
    closed_at TIMESTAMPTZ NULL,
    row_version BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (attempt_count <= max_attempts),
    CHECK (send_count <= max_sends),
    CHECK (status <> 'verified' OR verified_at IS NOT NULL)
);

CREATE UNIQUE INDEX uq_auth_challenges_issued
    ON auth_challenges (subject_type, subject_id, purpose)
    WHERE status = 'issued';

CREATE INDEX idx_auth_challenges_expiry
    ON auth_challenges (status, expires_at);

CREATE TABLE auth_verification_delivery_payloads (
    delivery_payload_id UUID PRIMARY KEY,
    challenge_id UUID NOT NULL REFERENCES auth_challenges (challenge_id),
    send_sequence SMALLINT NOT NULL CHECK (send_sequence > 0),
    payload_ciphertext BYTEA NULL,
    payload_key_id VARCHAR(128) NULL,
    aad_hash BYTEA NULL CHECK (aad_hash IS NULL OR octet_length(aad_hash) = 32),
    delivery_status VARCHAR(16) NOT NULL DEFAULT 'pending'
        CHECK (delivery_status IN ('pending', 'delivered', 'failed', 'expired', 'destroyed')),
    provider_request_id VARCHAR(128) NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    delivered_at TIMESTAMPTZ NULL,
    destroyed_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (challenge_id, send_sequence)
);

CREATE INDEX idx_auth_delivery_pending
    ON auth_verification_delivery_payloads (delivery_status, expires_at);

CREATE TABLE auth_identity_links (
    identity_link_id UUID PRIMARY KEY,
    identity_id UUID NOT NULL REFERENCES auth_identities (identity_id),
    identity_type VARCHAR(32) NOT NULL,
    user_id UUID NOT NULL,
    link_status VARCHAR(24) NOT NULL DEFAULT 'requested'
        CHECK (link_status IN ('requested', 'active', 'replaced', 'revoked', 'manual_review')),
    link_reason VARCHAR(24) NOT NULL DEFAULT 'signup'
        CHECK (link_reason IN ('signup', 'signin_link', 'phone_change', 'manual_operation')),
    proof_challenge_id UUID NULL REFERENCES auth_challenges (challenge_id),
    reauthentication_proof_id UUID NULL,
    previous_identity_link_id UUID NULL REFERENCES auth_identity_links (identity_link_id),
    requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    intent_expires_at TIMESTAMPTZ NULL,
    activated_at TIMESTAMPTZ NULL,
    closed_at TIMESTAMPTZ NULL,
    closed_reason VARCHAR(64) NULL,
    row_version BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (identity_link_id, identity_id, user_id),
    CHECK (link_status NOT IN ('replaced', 'revoked') OR closed_at IS NOT NULL),
    CHECK (previous_identity_link_id IS NULL OR previous_identity_link_id <> identity_link_id)
);

-- +goose StatementBegin
CREATE FUNCTION auth_fill_identity_link_type() RETURNS trigger AS $$
BEGIN
    IF NEW.identity_type IS NULL THEN
        SELECT identity_type INTO NEW.identity_type
        FROM auth_identities
        WHERE identity_id = NEW.identity_id;
    END IF;
    IF NEW.identity_type IS NULL THEN
        RAISE EXCEPTION 'identity link requires an existing identity type';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_auth_identity_links_type_snapshot
    BEFORE INSERT OR UPDATE OF identity_id, identity_type ON auth_identity_links
    FOR EACH ROW EXECUTE FUNCTION auth_fill_identity_link_type();

ALTER TABLE auth_identity_links
    ADD CONSTRAINT fk_auth_identity_links_identity_type
    FOREIGN KEY (identity_id, identity_type)
    REFERENCES auth_identities (identity_id, identity_type);

CREATE UNIQUE INDEX uq_auth_identity_links_identity_active
    ON auth_identity_links (identity_id)
    WHERE link_status = 'active';

CREATE UNIQUE INDEX uq_auth_identity_links_identity_requested
    ON auth_identity_links (identity_id)
    WHERE link_status = 'requested';

CREATE UNIQUE INDEX uq_auth_identity_links_user_primary_active
    ON auth_identity_links (user_id, identity_type)
    WHERE link_status = 'active' AND identity_type IN ('email', 'phone');

CREATE UNIQUE INDEX uq_auth_identity_links_previous
    ON auth_identity_links (previous_identity_link_id)
    WHERE previous_identity_link_id IS NOT NULL;

CREATE INDEX idx_auth_identity_links_user_status
    ON auth_identity_links (user_id, link_status);

CREATE INDEX idx_auth_identity_links_intent_expiry
    ON auth_identity_links (intent_expires_at)
    WHERE link_status = 'requested';

CREATE TABLE auth_password_resets (
    password_reset_id UUID PRIMARY KEY,
    intent_id UUID NULL REFERENCES auth_authentication_intents (intent_id),
    identity_id UUID NULL REFERENCES auth_identities (identity_id),
    challenge_id UUID NULL UNIQUE REFERENCES auth_challenges (challenge_id),
    status VARCHAR(24) NOT NULL DEFAULT 'requested'
        CHECK (status IN ('requested', 'challenge_verified', 'completed', 'expired', 'revoked')),
    reset_grant_hash BYTEA NULL CHECK (reset_grant_hash IS NULL OR octet_length(reset_grant_hash) = 32),
    reset_grant_key_version SMALLINT NULL,
    policy_version BIGINT NULL REFERENCES auth_policies (policy_version),
    expires_at TIMESTAMPTZ NOT NULL,
    challenge_verified_at TIMESTAMPTZ NULL,
    completed_at TIMESTAMPTZ NULL,
    row_version BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (status <> 'challenge_verified' OR (challenge_id IS NOT NULL AND reset_grant_hash IS NOT NULL AND challenge_verified_at IS NOT NULL))
);

CREATE UNIQUE INDEX uq_auth_password_resets_active
    ON auth_password_resets (identity_id)
    WHERE identity_id IS NOT NULL AND status IN ('requested', 'challenge_verified');

CREATE INDEX idx_auth_password_resets_status_expiry
    ON auth_password_resets (status, expires_at);

CREATE TABLE auth_user_auth_states (
    user_id UUID PRIMARY KEY,
    status VARCHAR(24) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'restricted', 'deactivated')),
    restriction_version BIGINT NOT NULL DEFAULT 1 CHECK (restriction_version > 0),
    reason_code VARCHAR(64) NULL,
    effective_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    source_event_id UUID NULL UNIQUE,
    row_version BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE auth_access_grants (
    access_grant_id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    roles TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    permissions TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    grant_version BIGINT NOT NULL DEFAULT 1 CHECK (grant_version > 0),
    grant_status VARCHAR(16) NOT NULL DEFAULT 'active' CHECK (grant_status IN ('active', 'revoked')),
    claims_hash BYTEA NULL CHECK (claims_hash IS NULL OR octet_length(claims_hash) = 32),
    source VARCHAR(32) NOT NULL DEFAULT 'registration',
    source_revision VARCHAR(128) NOT NULL DEFAULT 'initial',
    valid_from TIMESTAMPTZ NOT NULL DEFAULT now(),
    valid_until TIMESTAMPTZ NULL,
    changed_by_user_id UUID NULL,
    change_reason VARCHAR(500) NULL,
    revoked_at TIMESTAMPTZ NULL,
    row_version BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (access_grant_id, user_id, grant_version)
);

CREATE UNIQUE INDEX uq_auth_access_grants_user_active
    ON auth_access_grants (user_id)
    WHERE grant_status = 'active';

CREATE UNIQUE INDEX uq_auth_access_grants_user_version
    ON auth_access_grants (user_id, grant_version);

CREATE INDEX idx_auth_access_grants_user_status
    ON auth_access_grants (user_id, grant_status);

CREATE TABLE auth_sessions (
    session_id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    identity_id UUID NOT NULL REFERENCES auth_identities (identity_id),
    identity_link_id UUID NULL,
    authentication_method VARCHAR(32) NOT NULL
        CHECK (authentication_method IN ('registration_verified', 'email_password', 'phone_otp', 'provider', 'passkey')),
    last_authenticated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    access_grant_id UUID NULL,
    access_grant_version BIGINT NULL CHECK (access_grant_version IS NULL OR access_grant_version > 0),
    roles TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    grant_version BIGINT NOT NULL DEFAULT 1 CHECK (grant_version > 0),
    session_status VARCHAR(24) NOT NULL DEFAULT 'active'
        CHECK (session_status IN ('active', 'expired', 'revoked', 'reuse_detected')),
    client_channel VARCHAR(16) NOT NULL CHECK (client_channel IN ('web', 'mobile')),
    remember_me BOOLEAN NOT NULL DEFAULT FALSE,
    token_policy_version BIGINT NULL REFERENCES auth_policies (policy_version),
    issued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    idle_expires_at TIMESTAMPTZ NULL,
    absolute_expires_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NULL,
    revoked_at TIMESTAMPTZ NULL,
    revocation_reason VARCHAR(64) NULL,
    reuse_detected_at TIMESTAMPTZ NULL,
    row_version BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (session_id, user_id),
    CHECK (absolute_expires_at > issued_at),
    CHECK (client_channel <> 'web' OR (idle_expires_at IS NOT NULL AND idle_expires_at <= absolute_expires_at)),
    CHECK (session_status <> 'revoked' OR (revoked_at IS NOT NULL AND revocation_reason IS NOT NULL)),
    CHECK (session_status <> 'reuse_detected' OR reuse_detected_at IS NOT NULL)
);

ALTER TABLE auth_sessions
    ADD CONSTRAINT fk_auth_sessions_identity_link
    FOREIGN KEY (identity_link_id, identity_id, user_id)
    REFERENCES auth_identity_links (identity_link_id, identity_id, user_id),
    ADD CONSTRAINT fk_auth_sessions_access_grant
    FOREIGN KEY (access_grant_id, user_id, access_grant_version)
    REFERENCES auth_access_grants (access_grant_id, user_id, grant_version);

ALTER TABLE auth_authentication_intents
    ADD CONSTRAINT fk_auth_intents_consumed_session
    FOREIGN KEY (consumed_by_session_id) REFERENCES auth_sessions (session_id);

ALTER TABLE auth_registrations
    ADD CONSTRAINT fk_auth_registrations_session
    FOREIGN KEY (session_id) REFERENCES auth_sessions (session_id);

CREATE INDEX idx_auth_sessions_user_status
    ON auth_sessions (user_id, session_status);

CREATE INDEX idx_auth_sessions_expiry
    ON auth_sessions (session_status, absolute_expires_at);

CREATE TABLE auth_session_credentials (
    session_credential_id UUID PRIMARY KEY,
    session_id UUID NOT NULL REFERENCES auth_sessions (session_id),
    credential_type VARCHAR(32) NOT NULL CHECK (credential_type IN ('web_session_cookie', 'mobile_refresh_token')),
    credential_status VARCHAR(32) NOT NULL DEFAULT 'active'
        CHECK (credential_status IN ('active', 'rotated_pending_delivery', 'rotated', 'expired', 'revoked', 'reuse_detected')),
    secret_hash BYTEA NOT NULL UNIQUE CHECK (octet_length(secret_hash) = 32),
    secret_key_version SMALLINT NOT NULL DEFAULT 1,
    csrf_key_version SMALLINT NULL,
    refresh_family_id UUID NULL,
    rotated_from_credential_id UUID NULL UNIQUE,
    rotated_to_credential_id UUID NULL UNIQUE,
    issued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    rotated_at TIMESTAMPTZ NULL,
    delivery_recovery_expires_at TIMESTAMPTZ NULL,
    revoked_at TIMESTAMPTZ NULL,
    reuse_detected_at TIMESTAMPTZ NULL,
    row_version BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (
        (credential_type = 'web_session_cookie' AND refresh_family_id IS NULL)
        OR (credential_type = 'mobile_refresh_token' AND refresh_family_id IS NOT NULL)
    )
);

ALTER TABLE auth_session_credentials
    ADD CONSTRAINT fk_auth_session_credentials_rotated_from
    FOREIGN KEY (rotated_from_credential_id)
    REFERENCES auth_session_credentials (session_credential_id)
    DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT fk_auth_session_credentials_rotated_to
    FOREIGN KEY (rotated_to_credential_id)
    REFERENCES auth_session_credentials (session_credential_id)
    DEFERRABLE INITIALLY DEFERRED;

CREATE UNIQUE INDEX uq_auth_session_credentials_session_active
    ON auth_session_credentials (session_id, credential_type)
    WHERE credential_status = 'active';

CREATE UNIQUE INDEX uq_auth_session_credentials_family_active
    ON auth_session_credentials (refresh_family_id)
    WHERE credential_status = 'active' AND refresh_family_id IS NOT NULL;

CREATE INDEX idx_auth_session_credentials_hash
    ON auth_session_credentials (secret_hash);

CREATE INDEX idx_auth_session_credentials_family
    ON auth_session_credentials (refresh_family_id, issued_at)
    WHERE refresh_family_id IS NOT NULL;

CREATE TABLE auth_reauth_proofs (
    reauth_proof_id UUID PRIMARY KEY,
    proof_hash BYTEA NOT NULL UNIQUE CHECK (octet_length(proof_hash) = 32),
    proof_key_version SMALLINT NOT NULL DEFAULT 1,
    user_id UUID NOT NULL,
    session_id UUID NOT NULL,
    authenticated_identity_id UUID NULL REFERENCES auth_identities (identity_id),
    authentication_method VARCHAR(32) NOT NULL DEFAULT 'email_password',
    purpose VARCHAR(64) NOT NULL,
    authenticated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ NULL,
    invalidated_at TIMESTAMPTZ NULL,
    row_version BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (session_id, user_id) REFERENCES auth_sessions (session_id, user_id)
);

ALTER TABLE auth_identity_links
    ADD CONSTRAINT fk_auth_identity_links_reauth_proof
    FOREIGN KEY (reauthentication_proof_id) REFERENCES auth_reauth_proofs (reauth_proof_id);

CREATE INDEX idx_auth_reauth_proofs_expiry
    ON auth_reauth_proofs (expires_at)
    WHERE consumed_at IS NULL AND invalidated_at IS NULL;

CREATE INDEX idx_auth_reauth_proofs_session
    ON auth_reauth_proofs (session_id)
    WHERE consumed_at IS NULL AND invalidated_at IS NULL;

CREATE TABLE auth_idempotency_replay_payloads (
    replay_payload_id UUID PRIMARY KEY,
    payload_kind VARCHAR(48) NOT NULL
        CHECK (payload_kind IN ('mobile_refresh_response', 'reauthentication_credential_delivery', 'phone_replacement_credential_delivery')),
    payload_ciphertext BYTEA NULL,
    payload_key_id VARCHAR(128) NULL,
    binding_hash BYTEA NULL CHECK (binding_hash IS NULL OR octet_length(binding_hash) = 32),
    replay_count SMALLINT NOT NULL DEFAULT 0 CHECK (replay_count >= 0),
    expires_at TIMESTAMPTZ NOT NULL,
    destroyed_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_auth_replay_expiry
    ON auth_idempotency_replay_payloads (expires_at)
    WHERE destroyed_at IS NULL;

CREATE TABLE auth_idempotency_records (
    idempotency_record_id UUID PRIMARY KEY,
    operation VARCHAR(64) NOT NULL,
    scope_hash BYTEA NOT NULL CHECK (octet_length(scope_hash) = 32),
    key_hash BYTEA NOT NULL CHECK (octet_length(key_hash) = 32),
    request_hash BYTEA NOT NULL CHECK (octet_length(request_hash) = 32),
    status VARCHAR(16) NOT NULL DEFAULT 'processing' CHECK (status IN ('processing', 'completed', 'failed')),
    resource_type VARCHAR(64) NULL,
    resource_id UUID NULL,
    result_code VARCHAR(64) NULL,
    replay_payload_id UUID NULL UNIQUE REFERENCES auth_idempotency_replay_payloads (replay_payload_id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    UNIQUE (operation, scope_hash, key_hash)
);

CREATE INDEX idx_auth_idempotency_expiry
    ON auth_idempotency_records (expires_at);

CREATE UNIQUE INDEX uq_auth_registrations_completion_processing
    ON auth_idempotency_records (operation, resource_type, resource_id)
    WHERE operation = 'complete_registration' AND resource_type = 'Registration' AND status = 'processing';

ALTER TABLE auth_registrations
    ADD CONSTRAINT fk_auth_registrations_completion_idempotency
    FOREIGN KEY (completion_idempotency_record_id)
    REFERENCES auth_idempotency_records (idempotency_record_id)
    ON DELETE SET NULL;

CREATE TABLE auth_outbox_events (
    event_id UUID PRIMARY KEY,
    aggregate_type VARCHAR(64) NOT NULL,
    aggregate_id UUID NOT NULL,
    aggregate_version BIGINT NOT NULL,
    event_type VARCHAR(128) NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    correlation_id UUID NOT NULL,
    causation_id UUID NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    publish_status VARCHAR(16) NOT NULL DEFAULT 'pending'
        CHECK (publish_status IN ('pending', 'publishing', 'published', 'dead_letter')),
    publish_attempts INTEGER NOT NULL DEFAULT 0 CHECK (publish_attempts >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_owner VARCHAR(128) NULL,
    lease_until TIMESTAMPTZ NULL,
    published_at TIMESTAMPTZ NULL,
    last_error_code VARCHAR(64) NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (aggregate_type, aggregate_id, aggregate_version, event_type),
    CHECK (publish_status <> 'publishing' OR (lease_owner IS NOT NULL AND lease_until IS NOT NULL)),
    CHECK (publish_status <> 'published' OR published_at IS NOT NULL)
);

CREATE INDEX idx_auth_outbox_claim
    ON auth_outbox_events (publish_status, next_attempt_at, lease_until, created_at);

CREATE TABLE auth_inbox_messages (
    consumer_name VARCHAR(64) NOT NULL,
    source_event_id UUID NOT NULL,
    message_type VARCHAR(128) NOT NULL,
    schema_version SMALLINT NOT NULL,
    business_key UUID NOT NULL,
    link_request_id UUID NULL,
    causation_id UUID NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    payload_hash BYTEA NULL CHECK (payload_hash IS NULL OR octet_length(payload_hash) = 32),
    process_status VARCHAR(16) NOT NULL DEFAULT 'received'
        CHECK (process_status IN ('received', 'deferred', 'processed', 'rejected', 'dead_letter')),
    process_attempts INTEGER NOT NULL DEFAULT 0 CHECK (process_attempts >= 0),
    next_attempt_at TIMESTAMPTZ NULL,
    last_error_code VARCHAR(64) NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at TIMESTAMPTZ NULL,
    PRIMARY KEY (consumer_name, source_event_id),
    CHECK (
        message_type NOT IN ('User.AuthLinkRequested', 'User.AuthLinkRejected')
        OR (schema_version = 1 AND link_request_id IS NOT NULL AND causation_id IS NOT NULL)
    )
);

CREATE INDEX idx_auth_inbox_ready
    ON auth_inbox_messages (process_status, next_attempt_at, received_at)
    WHERE process_status IN ('received', 'deferred');

CREATE INDEX idx_auth_inbox_business_key
    ON auth_inbox_messages (consumer_name, business_key, link_request_id);

CREATE TABLE auth_manual_actions (
    manual_action_id UUID PRIMARY KEY,
    operator_user_id UUID NOT NULL,
    case_id VARCHAR(128) NOT NULL,
    target_type VARCHAR(32) NOT NULL CHECK (target_type IN ('identity', 'identity_link', 'session')),
    target_id VARCHAR(128) NOT NULL,
    action VARCHAR(32) NOT NULL CHECK (action IN ('unlock_identity', 'revoke_identity_link', 'approve_relink', 'revoke_sessions')),
    reason_code VARCHAR(64) NOT NULL,
    approval_id VARCHAR(128) NOT NULL,
    evidence_ref VARCHAR(512) NOT NULL,
    expected_target_version BIGINT NOT NULL CHECK (expected_target_version >= 0),
    target_version BIGINT NOT NULL CHECK (target_version >= 0),
    status VARCHAR(16) NOT NULL DEFAULT 'completed' CHECK (status IN ('completed')),
    idempotency_record_id UUID NULL UNIQUE REFERENCES auth_idempotency_records (idempotency_record_id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_auth_manual_actions_target
    ON auth_manual_actions (target_type, target_id, created_at DESC);

-- +goose StatementBegin
CREATE FUNCTION auth_prevent_identity_owner_change() RETURNS trigger AS $$
BEGIN
    IF OLD.owner_user_id IS NOT NULL AND NEW.owner_user_id IS DISTINCT FROM OLD.owner_user_id THEN
        RAISE EXCEPTION 'identity owner cannot be changed';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_auth_identities_owner_immutable
    BEFORE UPDATE OF owner_user_id ON auth_identities
    FOR EACH ROW EXECUTE FUNCTION auth_prevent_identity_owner_change();
