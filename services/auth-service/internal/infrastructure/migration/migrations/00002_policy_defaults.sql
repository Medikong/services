-- +goose Up
-- Keep storage bundles explicit while normalizing their JSON shape to the
-- public global-policy format. These defaults are the approved MVP policy.
UPDATE auth_policies
SET rules = '{"failureThreshold":5,"windowSeconds":900,"lockSeconds":900,"resetFailureOnSuccess":true}'::jsonb,
    updated_at = now()
WHERE policy_name = 'login_lock' AND status = 'active';

UPDATE auth_policies
SET rules = '{"webIdleSeconds":43200,"webAbsoluteSeconds":1209600,"mobileAccessSeconds":900,"mobileRefreshSeconds":1209600,"webRememberMeSeconds":2592000,"internalContextSeconds":900}'::jsonb,
    updated_at = now()
WHERE policy_name = 'session_ttl' AND status = 'active';

UPDATE auth_policies
SET rules = '{"enabled":true,"reuseAction":"revoke_family_and_session"}'::jsonb,
    updated_at = now()
WHERE policy_name = 'refresh_rotation' AND status = 'active';

UPDATE auth_policies
SET rules = '[
  {"purpose":"signup_email","channel":"email_code","ttlSeconds":600,"maxAttempts":5,"maxSends":3,"resendIntervalSeconds":60},
  {"purpose":"signup_phone","channel":"sms_code","ttlSeconds":600,"maxAttempts":5,"maxSends":3,"resendIntervalSeconds":60},
  {"purpose":"phone_signin","channel":"sms_code","ttlSeconds":600,"maxAttempts":5,"maxSends":3,"resendIntervalSeconds":60},
  {"purpose":"password_reset","channel":"email_code","ttlSeconds":600,"maxAttempts":5,"maxSends":3,"resendIntervalSeconds":60},
  {"purpose":"identity_link","channel":"sms_code","ttlSeconds":600,"maxAttempts":5,"maxSends":3,"resendIntervalSeconds":60},
  {"purpose":"phone_change","channel":"sms_code","ttlSeconds":600,"maxAttempts":5,"maxSends":3,"resendIntervalSeconds":60}
]'::jsonb,
    updated_at = now()
WHERE policy_name = 'verification_rules' AND status = 'active';

UPDATE auth_policies
SET rules = '[{"trigger":"password_reset","scopes":["user_sessions","refresh_family"]}]'::jsonb,
    updated_at = now()
WHERE policy_name = 'session_revocation_rules' AND status = 'active';

-- +goose Down
SELECT 1;
