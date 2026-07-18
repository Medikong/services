-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION auth_version_session_status_change()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.session_status IS DISTINCT FROM OLD.session_status THEN
        NEW.row_version := OLD.row_version + 1;
        NEW.updated_at := now();
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER trg_auth_version_session_status_change
BEFORE UPDATE OF session_status ON auth_sessions
FOR EACH ROW
EXECUTE FUNCTION auth_version_session_status_change();

-- +goose StatementBegin
CREATE FUNCTION auth_enqueue_session_status_projection()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.session_status IS NOT DISTINCT FROM OLD.session_status THEN
        RETURN NEW;
    END IF;

    INSERT INTO auth_outbox_events (
        event_id, aggregate_type, aggregate_id, aggregate_version, event_type,
        payload, correlation_id, occurred_at, publish_status, next_attempt_at
    ) VALUES (
        gen_random_uuid(), 'Session', NEW.session_id, NEW.row_version,
        'Auth.SessionStatusCacheUpdated',
        jsonb_build_object('status', NEW.session_status), NEW.session_id,
        now(), 'pending', now()
    ) ON CONFLICT (aggregate_type, aggregate_id, aggregate_version, event_type) DO NOTHING;

    IF NEW.session_status IN ('revoked', 'reuse_detected') THEN
        INSERT INTO auth_outbox_events (
            event_id, aggregate_type, aggregate_id, aggregate_version, event_type,
            payload, correlation_id, occurred_at, publish_status, next_attempt_at
        ) VALUES (
            gen_random_uuid(), 'Session', NEW.session_id, NEW.row_version,
            'Auth.SessionRevoked',
            jsonb_build_object('status', NEW.session_status), NEW.session_id,
            now(), 'pending', now()
        ) ON CONFLICT (aggregate_type, aggregate_id, aggregate_version, event_type) DO NOTHING;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER trg_auth_enqueue_session_status_projection
AFTER UPDATE OF session_status ON auth_sessions
FOR EACH ROW
EXECUTE FUNCTION auth_enqueue_session_status_projection();
