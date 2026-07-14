import sqlalchemy as sa
from alembic import op


class LegacyPaymentRowsError(RuntimeError):
    def __init__(self, issues: tuple[str, ...]) -> None:
        self.issues = issues
        super().__init__(
            "legacy payment rows violate the target schema: " + "; ".join(issues),
        )


def raise_for_invalid_legacy_rows(bind: sa.Connection) -> None:
    rows = bind.execute(
        sa.text(
            "SELECT record_id, issue FROM ("
            "SELECT order_id AS record_id, 'known_order_amount_negative' AS issue "
            "FROM known_orders WHERE amount < 0 UNION ALL "
            "SELECT id, 'payment_amount_negative' FROM payments WHERE amount < 0 "
            "UNION ALL SELECT id, 'payment_method_invalid' FROM payments "
            "WHERE method <> 'MOCK_CARD' UNION ALL "
            "SELECT id, 'payment_status_invalid' FROM payments "
            "WHERE status NOT IN ('APPROVED', 'FAILED') UNION ALL "
            "SELECT id, 'payment_terminal_timestamps_invalid' FROM payments WHERE "
            "NOT ((status = 'APPROVED' AND approved_at IS NOT NULL "
            "AND failed_at IS NULL) OR (status = 'FAILED' AND approved_at IS NULL "
            "AND failed_at IS NOT NULL)) UNION ALL "
            "SELECT payment.id, 'payment_order_missing' FROM payments payment "
            "LEFT JOIN known_orders known ON known.order_id = payment.order_id "
            "WHERE known.order_id IS NULL) invalid_rows ORDER BY issue, record_id",
        ),
    ).tuples()
    issues = tuple(f"{row[0]}:{row[1]}" for row in rows)
    if issues:
        raise LegacyPaymentRowsError(issues)


def apply_legacy_target_constraints() -> None:
    op.create_check_constraint(
        "ck_known_orders_amount_nonnegative",
        "known_orders",
        "amount >= 0",
    )
    op.create_check_constraint(
        "ck_payments_amount_nonnegative",
        "payments",
        "amount >= 0",
    )
    op.create_check_constraint(
        "ck_payments_method",
        "payments",
        "method = 'MOCK_CARD'",
    )
    op.create_check_constraint(
        "ck_payments_status",
        "payments",
        "status IN ('APPROVED', 'FAILED')",
    )
    op.create_check_constraint(
        "ck_payments_terminal_timestamps",
        "payments",
        "(status = 'APPROVED' AND approved_at IS NOT NULL AND failed_at IS NULL) "
        "OR (status = 'FAILED' AND approved_at IS NULL AND failed_at IS NOT NULL)",
    )
    op.create_foreign_key(
        "fk_payments_order_id",
        "payments",
        "known_orders",
        ["order_id"],
        ["order_id"],
    )
    op.create_unique_constraint(
        "uq_payments_order_id",
        "payments",
        ["order_id"],
    )
