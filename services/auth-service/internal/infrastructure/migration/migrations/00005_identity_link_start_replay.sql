-- +goose Up
-- Link-start commands need a durable, encrypted replay because they consume a
-- one-time reauthentication proof before creating the requested Link.
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

-- +goose Down
ALTER TABLE auth_idempotency_replay_payloads
    DROP CONSTRAINT auth_idempotency_replay_payloads_payload_kind_check;

ALTER TABLE auth_idempotency_replay_payloads
    ADD CONSTRAINT auth_idempotency_replay_payloads_payload_kind_check
    CHECK (payload_kind IN (
        'mobile_refresh_response',
        'reauthentication_credential_delivery',
        'phone_replacement_credential_delivery',
        'operator_policy_update'
    ));
