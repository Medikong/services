-- +goose Up
CREATE TABLE users (
    user_id uuid PRIMARY KEY,
    registration_id varchar(128) NOT NULL UNIQUE,
    account_status text NOT NULL CHECK (account_status IN ('active', 'restricted', 'deactivated')),
    private_name_ciphertext bytea NOT NULL,
    nickname varchar(50) NOT NULL CHECK (char_length(nickname) BETWEEN 1 AND 50),
    introduction varchar(500),
    profile_media_asset_id varchar(128),
    user_version bigint NOT NULL CHECK (user_version >= 1),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE INDEX users_account_status_updated_at_idx
    ON users (account_status, updated_at);

CREATE TABLE user_agreement_acceptances (
    user_id uuid NOT NULL REFERENCES users(user_id),
    agreement_code varchar(64) NOT NULL,
    agreement_version varchar(64) NOT NULL,
    accepted_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, agreement_code, agreement_version)
);

CREATE TABLE user_status_history (
    status_change_id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(user_id),
    previous_status text NOT NULL CHECK (previous_status IN ('active', 'restricted', 'deactivated')),
    changed_status text NOT NULL CHECK (changed_status IN ('active', 'restricted', 'deactivated')),
    reason_code varchar(64) NOT NULL,
    changed_by varchar(128) NOT NULL,
    changed_at timestamptz NOT NULL
);

CREATE INDEX user_status_history_user_changed_at_idx
    ON user_status_history (user_id, changed_at DESC);

CREATE TABLE user_idempotency_records (
    operation varchar(64) NOT NULL,
    scope_id varchar(128) NOT NULL,
    idempotency_key varchar(128) NOT NULL,
    request_hash bytea NOT NULL,
    result_type varchar(64),
    result_id varchar(128),
    result_version bigint,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (operation, scope_id, idempotency_key),
    CHECK ((result_type IS NULL AND result_id IS NULL AND result_version IS NULL) OR
           (result_type IS NOT NULL AND result_id IS NOT NULL AND result_version IS NOT NULL))
);

CREATE INDEX user_idempotency_records_expires_at_idx
    ON user_idempotency_records (expires_at);
