-- +goose Up

CREATE TABLE auth_virtual_verification_messages (
    challenge_id UUID PRIMARY KEY REFERENCES auth_challenges (challenge_id) ON DELETE CASCADE,
    channel VARCHAR(24) NOT NULL CHECK (channel IN ('email_code', 'sms_code')),
    challenge_version BIGINT NOT NULL,
    code_ciphertext BYTEA NULL,
    code_key_id VARCHAR(128) NULL,
    masked_destination VARCHAR(320) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'ready', 'destroyed')),
    expires_at TIMESTAMPTZ NOT NULL,
    destroyed_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_auth_virtual_messages_expiry
    ON auth_virtual_verification_messages (status, expires_at);
