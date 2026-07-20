-- +goose Up
-- Policy update responses are non-sensitive but are encrypted and replayed to
-- make an acknowledged operator retry return the same global snapshot.
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

-- +goose Down
ALTER TABLE auth_idempotency_replay_payloads
    DROP CONSTRAINT auth_idempotency_replay_payloads_payload_kind_check;

ALTER TABLE auth_idempotency_replay_payloads
    ADD CONSTRAINT auth_idempotency_replay_payloads_payload_kind_check
    CHECK (payload_kind IN (
        'mobile_refresh_response',
        'reauthentication_credential_delivery',
        'phone_replacement_credential_delivery'
    ));
