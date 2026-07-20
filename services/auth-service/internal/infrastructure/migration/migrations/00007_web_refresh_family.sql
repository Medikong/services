-- +goose Up
ALTER TABLE auth_session_credentials
    ADD COLUMN csrf_token_hash BYTEA NULL;

ALTER TABLE auth_session_credentials
    DROP CONSTRAINT IF EXISTS auth_session_credentials_credential_type_check,
    DROP CONSTRAINT IF EXISTS auth_session_credentials_check;

UPDATE auth_session_credentials
SET credential_type = 'web_refresh_cookie',
    refresh_family_id = gen_random_uuid(),
    csrf_token_hash = secret_hash
WHERE credential_type = 'web_session_cookie';

ALTER TABLE auth_session_credentials
    ADD CONSTRAINT auth_session_credentials_credential_type_check
        CHECK (credential_type IN ('web_refresh_cookie', 'mobile_refresh_token')),
    ADD CONSTRAINT auth_session_credentials_refresh_family_check
        CHECK (refresh_family_id IS NOT NULL),
    ADD CONSTRAINT auth_session_credentials_csrf_check
        CHECK (
            (credential_type = 'web_refresh_cookie' AND csrf_token_hash IS NOT NULL AND octet_length(csrf_token_hash) = 32)
            OR (credential_type = 'mobile_refresh_token' AND csrf_token_hash IS NULL)
        );

ALTER TABLE auth_idempotency_replay_payloads
    DROP CONSTRAINT auth_idempotency_replay_payloads_payload_kind_check;

ALTER TABLE auth_idempotency_replay_payloads
    ADD CONSTRAINT auth_idempotency_replay_payloads_payload_kind_check
    CHECK (payload_kind IN (
        'registration_completion',
        'mobile_refresh_response',
        'web_refresh_response',
        'reauthentication_credential_delivery',
        'phone_replacement_credential_delivery',
        'operator_policy_update',
        'identity_link_start_result'
    ));
