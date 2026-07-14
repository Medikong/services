-- +goose Up

CREATE TABLE coupon_campaigns (
    campaign_id TEXT PRIMARY KEY CHECK (campaign_id ~ '^camp_[A-Za-z0-9_-]{8,120}$'),
    display_name VARCHAR(120) NOT NULL,
    description VARCHAR(1000),
    status VARCHAR(24) NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'under_review', 'approved', 'rejected', 'held', 'active', 'ended')),
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ NOT NULL,
    claim_starts_at TIMESTAMPTZ,
    claim_ends_at TIMESTAMPTZ,
    current_policy_version INTEGER NOT NULL DEFAULT 1 CHECK (current_policy_version > 0),
    total_quantity BIGINT NOT NULL DEFAULT 0 CHECK (total_quantity >= 0),
    per_user_limit INTEGER NOT NULL DEFAULT 0 CHECK (per_user_limit >= 0),
    reserved_quantity BIGINT NOT NULL DEFAULT 0 CHECK (reserved_quantity >= 0),
    confirmed_quantity BIGINT NOT NULL DEFAULT 0 CHECK (confirmed_quantity >= 0),
    issuer_type VARCHAR(24) NOT NULL CHECK (issuer_type IN ('platform', 'seller', 'partnership', 'compensation')),
    issuer_ref JSONB NOT NULL,
    funder_type VARCHAR(24) NOT NULL CHECK (funder_type IN ('platform', 'seller', 'joint', 'compensation')),
    funder_ref JSONB,
    platform_share_percentage NUMERIC(5,2),
    approval_ref VARCHAR(200),
    owner_snapshot JSONB NOT NULL,
    external_business_ref VARCHAR(200),
    version BIGINT NOT NULL DEFAULT 0 CHECK (version >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (starts_at < ends_at),
    CHECK (claim_starts_at IS NULL OR claim_ends_at IS NULL OR claim_starts_at < claim_ends_at),
    CHECK ((total_quantity = 0 AND per_user_limit = 0) OR (total_quantity > 0 AND per_user_limit > 0)),
    CHECK (reserved_quantity + confirmed_quantity <= total_quantity)
);

CREATE INDEX idx_coupon_campaigns_status_period
    ON coupon_campaigns (status, starts_at, ends_at);

CREATE TABLE coupon_campaign_policy_versions (
    campaign_id TEXT NOT NULL REFERENCES coupon_campaigns (campaign_id) ON DELETE RESTRICT,
    policy_version INTEGER NOT NULL CHECK (policy_version > 0),
    effective_at TIMESTAMPTZ NOT NULL,
    issuer_and_funding JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (campaign_id, policy_version)
);

CREATE INDEX idx_coupon_campaign_policy_versions_effective
    ON coupon_campaign_policy_versions (campaign_id, effective_at DESC, policy_version DESC);

CREATE TABLE coupon_benefits (
    benefit_id TEXT PRIMARY KEY,
    campaign_id TEXT NOT NULL REFERENCES coupon_campaigns (campaign_id) ON DELETE RESTRICT,
    policy_version INTEGER NOT NULL CHECK (policy_version > 0),
    benefit_type VARCHAR(24) NOT NULL CHECK (benefit_type IN ('fixed_amount', 'percentage', 'shipping_fee')),
    amount NUMERIC(18,4),
    percentage NUMERIC(5,2),
    max_discount_amount NUMERIC(18,4),
    currency CHAR(3),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (campaign_id, policy_version, benefit_id),
    CHECK (amount IS NULL OR amount >= 0),
    CHECK (percentage IS NULL OR (percentage >= 0 AND percentage <= 100)),
    CHECK (max_discount_amount IS NULL OR max_discount_amount >= 0),
    CHECK (
        (benefit_type = 'fixed_amount' AND amount IS NOT NULL AND currency IS NOT NULL) OR
        (benefit_type = 'percentage' AND percentage IS NOT NULL) OR
        (benefit_type = 'shipping_fee')
    )
);

CREATE TABLE coupon_applicability_policies (
    policy_id TEXT PRIMARY KEY,
    campaign_id TEXT NOT NULL REFERENCES coupon_campaigns (campaign_id) ON DELETE RESTRICT,
    policy_version INTEGER NOT NULL CHECK (policy_version > 0),
    policy_schema_version SMALLINT NOT NULL DEFAULT 1 CHECK (policy_schema_version = 1),
    target_type VARCHAR(64) NOT NULL,
    target_ref VARCHAR(200) NOT NULL,
    inclusion VARCHAR(8) NOT NULL CHECK (inclusion IN ('include', 'exclude')),
    condition_type VARCHAR(64),
    condition_value JSONB,
    effective_from TIMESTAMPTZ NOT NULL,
    snapshot_label VARCHAR(160),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (campaign_id, policy_version, policy_id)
);

CREATE INDEX idx_coupon_applicability_lookup
    ON coupon_applicability_policies (campaign_id, policy_version, target_type, target_ref);

CREATE TABLE coupon_quantity_reservations (
    campaign_id TEXT NOT NULL REFERENCES coupon_campaigns (campaign_id) ON DELETE RESTRICT,
    issue_request_id TEXT NOT NULL CHECK (issue_request_id ~ '^ireq_[A-Za-z0-9_-]{8,120}$'),
    quantity BIGINT NOT NULL CHECK (quantity > 0),
    state VARCHAR(16) NOT NULL CHECK (state IN ('reserved', 'confirmed', 'released')),
    result_ref VARCHAR(200) NOT NULL,
    reserved_at TIMESTAMPTZ NOT NULL,
    decided_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (campaign_id, issue_request_id),
    CHECK ((state = 'reserved' AND decided_at IS NULL) OR (state IN ('confirmed', 'released') AND decided_at IS NOT NULL))
);

CREATE TABLE coupon_code_batches (
    code_batch_id TEXT PRIMARY KEY,
    campaign_id TEXT NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'active' CHECK (status IN ('draft', 'active', 'closed', 'revoked')),
    format VARCHAR(64) NOT NULL,
    quantity BIGINT NOT NULL CHECK (quantity >= 0),
    created_count BIGINT NOT NULL DEFAULT 0 CHECK (created_count >= 0 AND created_count <= quantity),
    distribution_channel VARCHAR(64),
    creator_ref VARCHAR(200) NOT NULL,
    version BIGINT NOT NULL DEFAULT 0 CHECK (version >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE coupon_codes (
    code_id TEXT PRIMARY KEY,
    code_batch_id TEXT NOT NULL REFERENCES coupon_code_batches (code_batch_id) ON DELETE RESTRICT,
    campaign_id TEXT NOT NULL,
    code_hash BYTEA NOT NULL UNIQUE,
    hash_version SMALLINT NOT NULL DEFAULT 1,
    normalization_version SMALLINT NOT NULL DEFAULT 1,
    code_suffix VARCHAR(16),
    status VARCHAR(16) NOT NULL DEFAULT 'available'
        CHECK (status IN ('available', 'reserved', 'redeemed', 'expired', 'discarded')),
    reserved_issue_request_id TEXT,
    reserved_until TIMESTAMPTZ,
    redeemed_user_coupon_id TEXT,
    redeemed_at TIMESTAMPTZ,
    version BIGINT NOT NULL DEFAULT 0 CHECK (version >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (status <> 'reserved' OR (reserved_issue_request_id IS NOT NULL AND reserved_until IS NOT NULL)),
    CHECK (status <> 'redeemed' OR (redeemed_user_coupon_id IS NOT NULL AND redeemed_at IS NOT NULL))
);

CREATE INDEX idx_coupon_codes_hash_cover
    ON coupon_codes (code_hash) INCLUDE (status, campaign_id, reserved_until);
CREATE UNIQUE INDEX uq_coupon_codes_active_issue_reservation
    ON coupon_codes (reserved_issue_request_id)
    WHERE status = 'reserved';

CREATE TABLE coupon_issue_requests (
    issue_request_id TEXT PRIMARY KEY CHECK (issue_request_id ~ '^ireq_[A-Za-z0-9_-]{8,120}$'),
    campaign_id TEXT NOT NULL,
    user_id VARCHAR(128) NOT NULL,
    business_key VARCHAR(200) NOT NULL,
    source_type VARCHAR(24) NOT NULL CHECK (source_type IN ('claim', 'redeem_code', 'bulk', 'system_grant', 'operator_grant')),
    source_ref VARCHAR(200) NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'accepted'
        CHECK (status IN ('accepted', 'pending', 'processing', 'failed_retryable', 'retry_pending', 'rejected', 'failed_final', 'completed')),
    user_coupon_id TEXT,
    failure_code VARCHAR(80),
    retry_count INTEGER NOT NULL DEFAULT 0 CHECK (retry_count >= 0),
    next_attempt_at TIMESTAMPTZ,
    issuer_and_funding_snapshot JSONB NOT NULL,
    policy_snapshot JSONB NOT NULL,
    approval_ref VARCHAR(200),
    result_ref VARCHAR(200),
    lease_owner VARCHAR(200),
    lease_until TIMESTAMPTZ,
    version BIGINT NOT NULL DEFAULT 0 CHECK (version >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (campaign_id, user_id, business_key),
    CHECK (status <> 'completed' OR user_coupon_id IS NOT NULL)
);

CREATE INDEX idx_coupon_issue_requests_worker
    ON coupon_issue_requests (status, next_attempt_at, issue_request_id);
CREATE INDEX idx_coupon_issue_requests_user_created
    ON coupon_issue_requests (user_id, created_at DESC);

CREATE TABLE user_coupons (
    user_coupon_id TEXT PRIMARY KEY CHECK (user_coupon_id ~ '^ucpn_[A-Za-z0-9_-]{8,120}$'),
    campaign_id TEXT NOT NULL,
    policy_version INTEGER NOT NULL CHECK (policy_version > 0),
    user_id VARCHAR(128) NOT NULL,
    issue_request_id TEXT NOT NULL UNIQUE,
    status VARCHAR(16) NOT NULL DEFAULT 'granted' CHECK (status IN ('granted', 'expired', 'revoked')),
    usable_from TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    grant_snapshot JSONB NOT NULL,
    result_ref VARCHAR(200) NOT NULL,
    expiry_lease_owner VARCHAR(200),
    expiry_lease_until TIMESTAMPTZ,
    expiry_attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (expiry_attempt_count >= 0),
    expiry_next_attempt_at TIMESTAMPTZ,
    version BIGINT NOT NULL DEFAULT 0 CHECK (version >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (usable_from < expires_at)
);

CREATE INDEX idx_user_coupons_user_status_expiry
    ON user_coupons (user_id, status, expires_at);
CREATE INDEX idx_user_coupons_expiry_worker
    ON user_coupons (status, expires_at, user_coupon_id)
    WHERE status = 'granted';

CREATE TABLE coupon_redemptions (
    redemption_id TEXT PRIMARY KEY CHECK (redemption_id ~ '^redm_[A-Za-z0-9_-]{8,120}$'),
    user_coupon_id TEXT NOT NULL,
    campaign_id TEXT NOT NULL,
    user_id VARCHAR(128) NOT NULL,
    order_id VARCHAR(200) NOT NULL,
    operation_type VARCHAR(24) NOT NULL CHECK (operation_type IN ('validate', 'reserve', 'confirm', 'release', 'reclaim')),
    business_key VARCHAR(200) NOT NULL,
    status VARCHAR(16) NOT NULL CHECK (status IN ('evaluated', 'rejected', 'reserved', 'confirmed', 'released', 'reclaimed')),
    reason_code VARCHAR(80),
    policy_version INTEGER NOT NULL CHECK (policy_version > 0),
    order_snapshot JSONB NOT NULL,
    order_snapshot_hash VARCHAR(80) NOT NULL,
    evaluated_at TIMESTAMPTZ NOT NULL,
    discount_amount NUMERIC(18,4) NOT NULL CHECK (discount_amount >= 0),
    final_order_amount NUMERIC(18,4) NOT NULL CHECK (final_order_amount >= 0),
    currency CHAR(3) NOT NULL,
    cost_attribution JSONB NOT NULL DEFAULT '[]'::jsonb,
    reserved_until TIMESTAMPTZ,
    confirmed_at TIMESTAMPTZ,
    released_at TIMESTAMPTZ,
    reclaimed_at TIMESTAMPTZ,
    result_ref JSONB NOT NULL,
    result_snapshot JSONB,
    version BIGINT NOT NULL DEFAULT 0 CHECK (version >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (order_id, user_coupon_id, business_key),
    CHECK (status <> 'reserved' OR reserved_until IS NOT NULL),
    CHECK (status <> 'confirmed' OR confirmed_at IS NOT NULL),
    CHECK (status <> 'released' OR released_at IS NOT NULL),
    CHECK (status <> 'reclaimed' OR reclaimed_at IS NOT NULL)
);

CREATE UNIQUE INDEX uq_coupon_redemptions_consuming_user_coupon
    ON coupon_redemptions (user_coupon_id)
    WHERE status IN ('reserved', 'confirmed', 'reclaimed');
CREATE INDEX idx_coupon_redemptions_order_status
    ON coupon_redemptions (order_id, status);
CREATE INDEX idx_coupon_redemptions_coupon_created
    ON coupon_redemptions (user_coupon_id, created_at DESC);

CREATE TABLE bulk_coupon_issue_jobs (
    bulk_job_id TEXT PRIMARY KEY CHECK (bulk_job_id ~ '^bjob_[A-Za-z0-9_-]{8,120}$'),
    campaign_id TEXT NOT NULL,
    owner_service_id VARCHAR(200) NOT NULL,
    audience_definition_ref VARCHAR(200) NOT NULL,
    audience_snapshot JSONB NOT NULL,
    evaluation_as_of TIMESTAMPTZ NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'registered'
        CHECK (status IN ('registered', 'running', 'completed', 'completed_with_failures', 'failed')),
	planning_complete BOOLEAN NOT NULL DEFAULT FALSE,
    target_count BIGINT NOT NULL DEFAULT 0 CHECK (target_count >= 0),
    succeeded_count BIGINT NOT NULL DEFAULT 0 CHECK (succeeded_count >= 0),
    rejected_count BIGINT NOT NULL DEFAULT 0 CHECK (rejected_count >= 0),
    failed_count BIGINT NOT NULL DEFAULT 0 CHECK (failed_count >= 0),
    operation_request_ref VARCHAR(200) NOT NULL,
    approval_ref VARCHAR(200) NOT NULL,
    lease_owner VARCHAR(200),
    lease_until TIMESTAMPTZ,
    next_attempt_at TIMESTAMPTZ,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    version BIGINT NOT NULL DEFAULT 0 CHECK (version >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	CHECK (succeeded_count + rejected_count + failed_count <= target_count),
	CHECK (status NOT IN ('completed', 'completed_with_failures') OR (
		planning_complete AND succeeded_count + rejected_count + failed_count = target_count
	))
);

CREATE INDEX idx_bulk_coupon_issue_jobs_worker
    ON bulk_coupon_issue_jobs (status, next_attempt_at, created_at);
CREATE UNIQUE INDEX uq_bulk_coupon_issue_jobs_operation_request
    ON bulk_coupon_issue_jobs (operation_request_ref);

CREATE TABLE coupon_operational_controls (
    control_id TEXT PRIMARY KEY CHECK (control_id ~ '^ctrl_[A-Za-z0-9_-]{8,120}$'),
    active BOOLEAN NOT NULL,
    effective_from TIMESTAMPTZ NOT NULL,
    block_issuance BOOLEAN NOT NULL DEFAULT FALSE,
    block_redemption BOOLEAN NOT NULL DEFAULT FALSE,
    notice_message VARCHAR(500),
    notice_active BOOLEAN NOT NULL DEFAULT FALSE,
    notice_effective_from TIMESTAMPTZ,
    operation_request_ref VARCHAR(200) NOT NULL,
    approval_ref VARCHAR(200) NOT NULL,
    reason_code VARCHAR(80),
    version BIGINT NOT NULL DEFAULT 0 CHECK (version >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (block_issuance OR block_redemption OR notice_message IS NOT NULL),
    CHECK (NOT notice_active OR (notice_message IS NOT NULL AND notice_effective_from IS NOT NULL))
);

CREATE TABLE coupon_operational_scopes (
    control_id TEXT NOT NULL REFERENCES coupon_operational_controls (control_id) ON DELETE RESTRICT,
    scope_type VARCHAR(24) NOT NULL CHECK (scope_type IN ('campaign', 'drop', 'user_group')),
    scope_ref VARCHAR(200) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (control_id, scope_type, scope_ref)
);

CREATE INDEX idx_coupon_operational_scope_lookup
    ON coupon_operational_scopes (scope_type, scope_ref, control_id);

CREATE TABLE coupon_event_recoveries (
    recovery_id TEXT PRIMARY KEY CHECK (recovery_id ~ '^rcvy_[A-Za-z0-9_-]{8,120}$'),
    redemption_id TEXT NOT NULL CHECK (redemption_id ~ '^redm_[A-Za-z0-9_-]{8,120}$'),
    original_operation_type VARCHAR(24) NOT NULL CHECK (original_operation_type IN ('reserve', 'confirm', 'release', 'reclaim')),
    original_payload_ref VARCHAR(200) NOT NULL,
    original_payload_hash VARCHAR(80) NOT NULL,
    business_key VARCHAR(200) NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'recorded'
        CHECK (status IN ('recorded', 'retry_pending', 'retrying', 'retry_failed', 'completed', 'failed_final')),
    current_attempt_id TEXT,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at TIMESTAMPTZ,
    result_kind VARCHAR(24) CHECK (result_kind IN ('transitioned', 'already_applied', 'failed')),
    result_ref VARCHAR(200),
    failure_code VARCHAR(80),
    operation_request_ref VARCHAR(200),
    approval_ref VARCHAR(200),
    lease_owner VARCHAR(200),
    lease_until TIMESTAMPTZ,
    version BIGINT NOT NULL DEFAULT 0 CHECK (version >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (recovery_id, business_key),
    CHECK (result_kind NOT IN ('transitioned', 'already_applied') OR result_ref IS NOT NULL),
    CHECK (status <> 'completed' OR (result_kind IN ('transitioned', 'already_applied') AND result_ref IS NOT NULL)),
    CHECK (status NOT IN ('retry_failed', 'failed_final') OR failure_code IS NOT NULL)
);

CREATE INDEX idx_coupon_event_recoveries_worker
    ON coupon_event_recoveries (status, next_attempt_at, recovery_id);

CREATE TABLE coupon_recovery_attempts (
    recovery_id TEXT NOT NULL REFERENCES coupon_event_recoveries (recovery_id) ON DELETE RESTRICT,
    attempt_id TEXT NOT NULL CHECK (attempt_id ~ '^att_[A-Za-z0-9_-]{8,120}$'),
    business_key VARCHAR(200) NOT NULL,
    status VARCHAR(24) NOT NULL CHECK (status IN ('retry_pending', 'retrying', 'completed', 'failed')),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    result_kind VARCHAR(24) CHECK (result_kind IN ('transitioned', 'already_applied', 'failed')),
    result_ref VARCHAR(200),
    failure_code VARCHAR(80),
    retryable BOOLEAN,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (recovery_id, attempt_id, business_key),
    CHECK (result_kind NOT IN ('transitioned', 'already_applied') OR result_ref IS NOT NULL),
    CHECK (status <> 'completed' OR (result_kind IN ('transitioned', 'already_applied') AND result_ref IS NOT NULL)),
    CHECK (status <> 'failed' OR (result_kind = 'failed' AND failure_code IS NOT NULL AND retryable IS NOT NULL))
);

CREATE UNIQUE INDEX uq_coupon_recovery_attempts_active
    ON coupon_recovery_attempts (recovery_id)
    WHERE status = 'retrying';

CREATE TABLE coupon_idempotency_records (
    operation_type VARCHAR(80) NOT NULL,
    business_key VARCHAR(200) NOT NULL,
    owner_type VARCHAR(80) NOT NULL,
    owner_id VARCHAR(200) NOT NULL,
    request_hash BYTEA NOT NULL CHECK (octet_length(request_hash) = 32),
    status VARCHAR(24) NOT NULL CHECK (status IN ('processing', 'completed', 'failed_final')),
    result_ref VARCHAR(200),
    response_snapshot JSONB,
    locked_until TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (operation_type, business_key),
    CHECK (status <> 'completed' OR result_ref IS NOT NULL),
    CHECK (
        (status = 'processing' AND locked_until IS NOT NULL AND completed_at IS NULL)
        OR (status IN ('completed', 'failed_final') AND locked_until IS NULL AND completed_at IS NOT NULL)
    ),
    CHECK (expires_at > created_at)
);

CREATE INDEX idx_coupon_idempotency_expiry
    ON coupon_idempotency_records (expires_at);

CREATE TABLE domain_outbox (
    event_id UUID PRIMARY KEY,
    event_sequence BIGINT GENERATED ALWAYS AS IDENTITY UNIQUE,
    event_type VARCHAR(160) NOT NULL,
    event_document_id VARCHAR(24) NOT NULL CHECK (event_document_id ~ '^EVT\.A\.19-[0-9]{2}$'),
    payload_schema_version SMALLINT NOT NULL DEFAULT 1 CHECK (payload_schema_version > 0),
    aggregate_type VARCHAR(80) NOT NULL,
    aggregate_id VARCHAR(200) NOT NULL,
    aggregate_version BIGINT NOT NULL CHECK (aggregate_version >= 0),
    correlation_id VARCHAR(200) NOT NULL,
    causation_id VARCHAR(200),
    trace_id VARCHAR(64),
    payload JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    publish_status VARCHAR(24) NOT NULL DEFAULT 'pending'
        CHECK (publish_status IN ('pending', 'publishing', 'published', 'dead_letter')),
    lease_owner VARCHAR(200),
    lease_until TIMESTAMPTZ,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    last_error_code VARCHAR(160),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_domain_outbox_publish
    ON domain_outbox (publish_status, next_attempt_at, occurred_at);
CREATE INDEX idx_domain_outbox_aggregate_order
    ON domain_outbox (aggregate_type, aggregate_id, aggregate_version, event_sequence, publish_status);

CREATE TABLE consumer_inbox (
    consumer_name VARCHAR(160) NOT NULL,
    event_id UUID NOT NULL,
    event_type VARCHAR(160) NOT NULL,
    payload_schema_version SMALLINT NOT NULL CHECK (payload_schema_version > 0),
    payload_hash BYTEA NOT NULL CHECK (octet_length(payload_hash) = 32),
    status VARCHAR(24) NOT NULL DEFAULT 'received'
        CHECK (status IN ('received', 'processed', 'failed_retryable', 'failed_final')),
    result_ref VARCHAR(200),
    failure_code VARCHAR(160),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at TIMESTAMPTZ,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer_name, event_id)
);

CREATE INDEX idx_consumer_inbox_retry
    ON consumer_inbox (status, next_attempt_at, received_at);

CREATE TABLE coupon_command_requests (
    command_request_id UUID PRIMARY KEY,
    command_document_id VARCHAR(24) NOT NULL CHECK (command_document_id ~ '^CMD\.A\.19-[0-9]{2}$'),
    policy_document_id VARCHAR(27) CHECK (policy_document_id ~ '^POLICY\.A\.19-[0-9]{2}$'),
    source_event_id UUID,
    aggregate_type VARCHAR(80) NOT NULL,
    aggregate_id VARCHAR(200) NOT NULL,
    business_key VARCHAR(200) NOT NULL,
    correlation_id VARCHAR(200) NOT NULL,
    causation_id VARCHAR(200),
    trace_id VARCHAR(200),
    payload JSONB NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'processing', 'completed', 'dead_letter')),
    lease_owner VARCHAR(200),
    lease_until TIMESTAMPTZ,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    result_ref VARCHAR(200),
    failure_code VARCHAR(160),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (policy_document_id, source_event_id, command_document_id, business_key)
);

CREATE INDEX idx_coupon_command_requests_worker
    ON coupon_command_requests (status, next_attempt_at, created_at);

CREATE TABLE coupon_quantity_ledger (
    ledger_id UUID PRIMARY KEY,
    campaign_id TEXT NOT NULL,
    issue_request_id TEXT NOT NULL,
    transition VARCHAR(24) NOT NULL CHECK (transition IN ('reserve', 'reject', 'confirm', 'release')),
    quantity BIGINT NOT NULL CHECK (quantity > 0),
    before_state VARCHAR(16),
    after_state VARCHAR(16) NOT NULL,
    result_ref VARCHAR(200) NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_coupon_quantity_ledger_request
    ON coupon_quantity_ledger (campaign_id, issue_request_id, occurred_at);

CREATE TABLE coupon_issue_ledger (
    ledger_id UUID PRIMARY KEY,
    issue_request_id TEXT NOT NULL,
    business_key VARCHAR(200) NOT NULL,
    event_type VARCHAR(160) NOT NULL,
    status VARCHAR(24) NOT NULL,
    result_ref VARCHAR(200),
    failure_code VARCHAR(80),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_coupon_issue_ledger_request
    ON coupon_issue_ledger (issue_request_id, occurred_at);

CREATE TABLE user_coupon_ledger (
    ledger_id UUID PRIMARY KEY,
    user_coupon_id TEXT NOT NULL,
    issue_request_id TEXT NOT NULL,
    event_type VARCHAR(160) NOT NULL,
    result_ref VARCHAR(200) NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_user_coupon_ledger_coupon
    ON user_coupon_ledger (user_coupon_id, occurred_at);

CREATE TABLE coupon_redemption_ledger (
    ledger_id UUID PRIMARY KEY,
    redemption_id TEXT NOT NULL,
    order_id VARCHAR(200) NOT NULL,
    user_coupon_id TEXT NOT NULL,
    event_type VARCHAR(160) NOT NULL,
    amount_snapshot JSONB NOT NULL,
    result_ref VARCHAR(200) NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_coupon_redemption_ledger_redemption
    ON coupon_redemption_ledger (redemption_id, occurred_at);

CREATE TABLE coupon_operation_ledger (
    ledger_id UUID PRIMARY KEY,
    control_id TEXT NOT NULL,
    scope JSONB NOT NULL,
    operation_request_ref VARCHAR(200) NOT NULL,
    approval_ref VARCHAR(200) NOT NULL,
    event_type VARCHAR(160) NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_coupon_operation_ledger_control
    ON coupon_operation_ledger (control_id, occurred_at);

CREATE TABLE coupon_recovery_ledger (
    ledger_id UUID PRIMARY KEY,
    recovery_id TEXT NOT NULL,
    attempt_id TEXT,
    business_key VARCHAR(200) NOT NULL,
    event_type VARCHAR(160) NOT NULL,
    result_ref VARCHAR(200),
    failure_code VARCHAR(80),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_coupon_recovery_ledger_recovery
    ON coupon_recovery_ledger (recovery_id, occurred_at);

CREATE TABLE bulk_coupon_issue_ledger (
    ledger_id UUID PRIMARY KEY,
    bulk_job_id TEXT NOT NULL,
    event_type VARCHAR(160) NOT NULL,
    status VARCHAR(32) NOT NULL,
    target_count BIGINT NOT NULL CHECK (target_count >= 0),
    succeeded_count BIGINT NOT NULL CHECK (succeeded_count >= 0),
    rejected_count BIGINT NOT NULL CHECK (rejected_count >= 0),
    failed_count BIGINT NOT NULL CHECK (failed_count >= 0),
    result_ref VARCHAR(200) NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_bulk_coupon_issue_ledger_job
    ON bulk_coupon_issue_ledger (bulk_job_id, occurred_at);
CREATE UNIQUE INDEX uq_bulk_coupon_issue_ledger_result
    ON bulk_coupon_issue_ledger (bulk_job_id, result_ref)
    WHERE event_type IN (
        'coupon.bulk_issue.result_aggregated',
        'coupon.bulk_issue.completed',
        'coupon.bulk_issue.completed_with_failures'
    );

-- RM.A.19-03 is intentionally absent until HOTSPOT.A.19-07 is resolved.
CREATE TABLE rm_user_coupon_wallet (
    user_id VARCHAR(128) NOT NULL,
    user_coupon_id TEXT NOT NULL,
    campaign_id TEXT NOT NULL,
    display_name VARCHAR(120) NOT NULL,
    benefit JSONB NOT NULL,
    display_status VARCHAR(16) NOT NULL,
    usable_from TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    last_event_id UUID NOT NULL,
    projection_version BIGINT NOT NULL CHECK (projection_version >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, user_coupon_id)
);

CREATE INDEX idx_rm_user_coupon_wallet_list
    ON rm_user_coupon_wallet (user_id, display_status, expires_at);

CREATE TABLE rm_coupon_details (
    user_coupon_id TEXT PRIMARY KEY,
    user_id VARCHAR(128) NOT NULL,
    campaign_id TEXT NOT NULL,
    policy_version INTEGER NOT NULL,
    detail JSONB NOT NULL,
    last_event_id UUID NOT NULL,
    projection_version BIGINT NOT NULL CHECK (projection_version >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_rm_coupon_details_campaign_policy
    ON rm_coupon_details (campaign_id, policy_version);

CREATE TABLE rm_coupon_performance_minutely (
    campaign_id TEXT NOT NULL,
    bucket_at TIMESTAMPTZ NOT NULL,
    requested_count BIGINT NOT NULL DEFAULT 0,
    issued_count BIGINT NOT NULL DEFAULT 0,
    rejected_count BIGINT NOT NULL DEFAULT 0,
    failed_count BIGINT NOT NULL DEFAULT 0,
    reserved_count BIGINT NOT NULL DEFAULT 0,
    confirmed_count BIGINT NOT NULL DEFAULT 0,
    released_count BIGINT NOT NULL DEFAULT 0,
    reclaimed_count BIGINT NOT NULL DEFAULT 0,
    confirmed_discount_amount NUMERIC(18,4) NOT NULL DEFAULT 0,
    reclaimed_discount_amount NUMERIC(18,4) NOT NULL DEFAULT 0,
    currency CHAR(3),
    last_event_id UUID NOT NULL,
    projection_version BIGINT NOT NULL CHECK (projection_version >= 0),
    PRIMARY KEY (campaign_id, bucket_at)
);

CREATE TABLE rm_coupon_failures (
    failure_id TEXT PRIMARY KEY,
    failure_kind VARCHAR(24) NOT NULL DEFAULT 'issue' CHECK (failure_kind IN ('issue', 'bulk', 'recovery')),
    failure_status VARCHAR(24) NOT NULL,
    business_key VARCHAR(200) NOT NULL,
    source_ref VARCHAR(200) NOT NULL,
    original_operation_type VARCHAR(24) CHECK (original_operation_type IN ('reserve', 'confirm', 'release', 'reclaim')),
    current_attempt_id TEXT,
    result_kind VARCHAR(24) CHECK (result_kind IN ('transitioned', 'already_applied', 'failed')),
    result_ref VARCHAR(200),
    failure_code VARCHAR(160) NOT NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ,
    last_event_id UUID NOT NULL,
    projection_version BIGINT NOT NULL CHECK (projection_version >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_rm_coupon_failures_retry
    ON rm_coupon_failures (failure_status, next_attempt_at);

CREATE TABLE rm_user_coupon_timeline (
    timeline_id UUID PRIMARY KEY,
    user_id VARCHAR(128) NOT NULL,
    user_coupon_id TEXT,
    event_type VARCHAR(160) NOT NULL,
    result_ref JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    last_event_id UUID NOT NULL UNIQUE,
    projection_version BIGINT NOT NULL CHECK (projection_version >= 0)
);

CREATE INDEX idx_rm_user_coupon_timeline_user
    ON rm_user_coupon_timeline (user_id, occurred_at DESC);

CREATE TABLE rm_coupon_incident_status (
    incident_key TEXT PRIMARY KEY,
    scope_type VARCHAR(24) NOT NULL,
    scope_ref VARCHAR(200) NOT NULL,
    status VARCHAR(24) NOT NULL,
    business_metrics JSONB NOT NULL DEFAULT '{}'::jsonb,
    observability_ref VARCHAR(200),
    observed_at TIMESTAMPTZ NOT NULL,
    last_event_id UUID NOT NULL,
    projection_version BIGINT NOT NULL CHECK (projection_version >= 0)
);

CREATE INDEX idx_rm_coupon_incident_scope
    ON rm_coupon_incident_status (scope_type, scope_ref, observed_at DESC);

CREATE TABLE rm_coupon_cost_attribution (
    attribution_id UUID PRIMARY KEY,
    order_id VARCHAR(200) NOT NULL,
    redemption_id TEXT NOT NULL,
    campaign_id TEXT NOT NULL,
    kind VARCHAR(16) NOT NULL CHECK (kind IN ('confirmed', 'reclaimed')),
    discount_amount NUMERIC(18,4) NOT NULL CHECK (discount_amount >= 0),
    currency CHAR(3) NOT NULL,
    cost_shares JSONB NOT NULL,
    settlement_ref VARCHAR(200),
    occurred_at TIMESTAMPTZ NOT NULL,
    last_event_id UUID NOT NULL UNIQUE,
    projection_version BIGINT NOT NULL CHECK (projection_version >= 0)
);

CREATE INDEX idx_rm_coupon_cost_order_redemption
    ON rm_coupon_cost_attribution (order_id, redemption_id);
CREATE INDEX idx_rm_coupon_cost_settlement
    ON rm_coupon_cost_attribution (settlement_ref)
    WHERE settlement_ref IS NOT NULL;

CREATE TABLE rm_coupon_read_only_notice (
    control_id TEXT NOT NULL,
    scope_type VARCHAR(24) NOT NULL,
    scope_ref VARCHAR(200) NOT NULL,
    message VARCHAR(500) NOT NULL,
    effective_from TIMESTAMPTZ NOT NULL,
    active BOOLEAN NOT NULL,
    last_event_id UUID NOT NULL,
    projection_version BIGINT NOT NULL CHECK (projection_version >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (control_id, scope_type, scope_ref)
);

CREATE INDEX idx_rm_coupon_notice_scope
    ON rm_coupon_read_only_notice (scope_type, scope_ref, effective_from);

-- +goose StatementBegin
CREATE FUNCTION coupon_reject_ledger_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'coupon ledgers are append-only';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER trg_coupon_quantity_ledger_append_only BEFORE UPDATE OR DELETE ON coupon_quantity_ledger
    FOR EACH ROW EXECUTE FUNCTION coupon_reject_ledger_mutation();
CREATE TRIGGER trg_coupon_issue_ledger_append_only BEFORE UPDATE OR DELETE ON coupon_issue_ledger
    FOR EACH ROW EXECUTE FUNCTION coupon_reject_ledger_mutation();
CREATE TRIGGER trg_user_coupon_ledger_append_only BEFORE UPDATE OR DELETE ON user_coupon_ledger
    FOR EACH ROW EXECUTE FUNCTION coupon_reject_ledger_mutation();
CREATE TRIGGER trg_coupon_redemption_ledger_append_only BEFORE UPDATE OR DELETE ON coupon_redemption_ledger
    FOR EACH ROW EXECUTE FUNCTION coupon_reject_ledger_mutation();
CREATE TRIGGER trg_coupon_operation_ledger_append_only BEFORE UPDATE OR DELETE ON coupon_operation_ledger
    FOR EACH ROW EXECUTE FUNCTION coupon_reject_ledger_mutation();
CREATE TRIGGER trg_coupon_recovery_ledger_append_only BEFORE UPDATE OR DELETE ON coupon_recovery_ledger
    FOR EACH ROW EXECUTE FUNCTION coupon_reject_ledger_mutation();
CREATE TRIGGER trg_bulk_coupon_issue_ledger_append_only BEFORE UPDATE OR DELETE ON bulk_coupon_issue_ledger
    FOR EACH ROW EXECUTE FUNCTION coupon_reject_ledger_mutation();

-- +goose Down

DROP TABLE IF EXISTS rm_coupon_read_only_notice;
DROP TABLE IF EXISTS rm_coupon_cost_attribution;
DROP TABLE IF EXISTS rm_coupon_incident_status;
DROP TABLE IF EXISTS rm_user_coupon_timeline;
DROP TABLE IF EXISTS rm_coupon_failures;
DROP TABLE IF EXISTS rm_coupon_performance_minutely;
DROP TABLE IF EXISTS rm_coupon_details;
DROP TABLE IF EXISTS rm_user_coupon_wallet;
DROP TRIGGER IF EXISTS trg_bulk_coupon_issue_ledger_append_only ON bulk_coupon_issue_ledger;
DROP TRIGGER IF EXISTS trg_coupon_recovery_ledger_append_only ON coupon_recovery_ledger;
DROP TRIGGER IF EXISTS trg_coupon_operation_ledger_append_only ON coupon_operation_ledger;
DROP TRIGGER IF EXISTS trg_coupon_redemption_ledger_append_only ON coupon_redemption_ledger;
DROP TRIGGER IF EXISTS trg_user_coupon_ledger_append_only ON user_coupon_ledger;
DROP TRIGGER IF EXISTS trg_coupon_issue_ledger_append_only ON coupon_issue_ledger;
DROP TRIGGER IF EXISTS trg_coupon_quantity_ledger_append_only ON coupon_quantity_ledger;
DROP FUNCTION IF EXISTS coupon_reject_ledger_mutation();
DROP TABLE IF EXISTS bulk_coupon_issue_ledger;
DROP TABLE IF EXISTS coupon_recovery_ledger;
DROP TABLE IF EXISTS coupon_operation_ledger;
DROP TABLE IF EXISTS coupon_redemption_ledger;
DROP TABLE IF EXISTS user_coupon_ledger;
DROP TABLE IF EXISTS coupon_issue_ledger;
DROP TABLE IF EXISTS coupon_quantity_ledger;
DROP TABLE IF EXISTS coupon_command_requests;
DROP TABLE IF EXISTS consumer_inbox;
DROP TABLE IF EXISTS domain_outbox;
DROP TABLE IF EXISTS coupon_idempotency_records;
DROP TABLE IF EXISTS coupon_recovery_attempts;
DROP TABLE IF EXISTS coupon_event_recoveries;
DROP TABLE IF EXISTS coupon_operational_scopes;
DROP TABLE IF EXISTS coupon_operational_controls;
DROP TABLE IF EXISTS bulk_coupon_issue_jobs;
DROP TABLE IF EXISTS coupon_redemptions;
DROP TABLE IF EXISTS user_coupons;
DROP TABLE IF EXISTS coupon_issue_requests;
DROP TABLE IF EXISTS coupon_codes;
DROP TABLE IF EXISTS coupon_code_batches;
DROP TABLE IF EXISTS coupon_quantity_reservations;
DROP TABLE IF EXISTS coupon_applicability_policies;
DROP TABLE IF EXISTS coupon_benefits;
DROP TABLE IF EXISTS coupon_campaign_policy_versions;
DROP TABLE IF EXISTS coupon_campaigns;
