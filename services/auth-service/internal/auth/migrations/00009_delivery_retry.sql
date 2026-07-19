-- +goose Up
ALTER TABLE auth_verification_delivery_payloads
    ADD COLUMN delivery_attempts INTEGER NOT NULL DEFAULT 0 CHECK (delivery_attempts >= 0),
    ADD COLUMN next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN lease_owner VARCHAR(128) NULL,
    ADD COLUMN lease_until TIMESTAMPTZ NULL,
    ADD COLUMN last_error_code VARCHAR(64) NULL;

CREATE INDEX idx_auth_delivery_claim
    ON auth_verification_delivery_payloads (delivery_status, next_attempt_at, lease_until, expires_at);
