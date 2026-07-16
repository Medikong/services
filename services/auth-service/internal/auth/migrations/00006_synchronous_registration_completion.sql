-- +goose Up
-- Registration completion is synchronous in the current behavior. Its short
-- credential-delivery replay is encrypted like refresh and reauth responses.
ALTER TABLE auth_idempotency_replay_payloads
    DROP CONSTRAINT auth_idempotency_replay_payloads_payload_kind_check;

ALTER TABLE auth_idempotency_replay_payloads
    ADD CONSTRAINT auth_idempotency_replay_payloads_payload_kind_check
    CHECK (payload_kind IN (
        'registration_completion',
        'mobile_refresh_response',
        'reauthentication_credential_delivery',
        'phone_replacement_credential_delivery',
        'operator_policy_update',
        'identity_link_start_result'
    ));

DROP TABLE IF EXISTS auth_inbox_messages;

-- +goose Down
ALTER TABLE auth_idempotency_replay_payloads
    DROP CONSTRAINT auth_idempotency_replay_payloads_payload_kind_check;

ALTER TABLE auth_idempotency_replay_payloads
    ADD CONSTRAINT auth_idempotency_replay_payloads_payload_kind_check
    CHECK (payload_kind IN (
        'mobile_refresh_response',
        'reauthentication_credential_delivery',
        'phone_replacement_credential_delivery',
        'operator_policy_update',
        'identity_link_start_result'
    ));
