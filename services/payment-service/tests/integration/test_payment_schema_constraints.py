import anyio
import pytest
from sqlalchemy import String, bindparam, text
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncEngine, create_async_engine

from tests.integration.migration_support import isolated_database, run_migration


@pytest.mark.anyio
async def test_concurrent_terminal_payment_inserts_allow_one_result_per_order() -> None:
    # Given
    async with isolated_database("payment_terminal") as database:
        migration = await run_migration(database.url, "upgrade", "head")
        assert migration.returncode == 0, migration.stderr
        engine = create_async_engine(database.url)
        async with engine.begin() as connection:
            await connection.execute(
                text(
                    "INSERT INTO known_orders (order_id, user_id, amount, created_at) "
                    "VALUES ('order-terminal', 'user-001', 50000, now())",
                ),
            )
        start = anyio.Event()
        inserted_statuses: list[str] = []

        # When
        async with anyio.create_task_group() as task_group:
            task_group.start_soon(
                _insert_terminal_payment,
                engine,
                start,
                inserted_statuses,
                "payment-approved",
                "APPROVED",
            )
            task_group.start_soon(
                _insert_terminal_payment,
                engine,
                start,
                inserted_statuses,
                "payment-failed",
                "FAILED",
            )
            start.set()

        # Then
        async with engine.connect() as connection:
            rows = (
                (
                    await connection.execute(
                        text(
                            "SELECT order_id, status FROM payments "
                            "WHERE order_id = 'order-terminal'",
                        ),
                    )
                )
                .tuples()
                .all()
            )
        await engine.dispose()
        assert len(inserted_statuses) == 1
        assert rows == [("order-terminal", inserted_statuses[0])]


@pytest.mark.anyio
async def test_refund_rejects_empty_idempotency_fingerprint() -> None:
    # Given
    async with isolated_database("refund_fingerprint") as database:
        migration = await run_migration(database.url, "upgrade", "head")
        assert migration.returncode == 0, migration.stderr
        engine = create_async_engine(database.url)
        await _seed_refund_parents(engine)

        # When
        with pytest.raises(IntegrityError):
            async with engine.begin() as connection:
                await connection.execute(
                    text(
                        "INSERT INTO refunds "
                        "(id, order_id, payment_id, user_id, amount, status, reason, "
                        "idempotency_fingerprint, attempts, created_at, updated_at) "
                        "VALUES ('refund-001', 'order-001', 'payment-001', 'user-001', "
                        "50000, 'REQUESTED', 'customer_cancelled', '', 0, now(), now())",
                    ),
                )

        # Then
        async with engine.connect() as connection:
            count = (
                await connection.execute(text("SELECT count(*) FROM refunds"))
            ).scalar_one()
        await engine.dispose()
        assert count == 0


@pytest.mark.anyio
@pytest.mark.parametrize(
    "duplicate_column",
    ["order_id", "payment_id", "idempotency_fingerprint"],
)
async def test_refund_keys_are_individually_unique(duplicate_column: str) -> None:
    # Given
    async with isolated_database(f"refund_unique_{duplicate_column}") as database:
        migration = await run_migration(database.url, "upgrade", "head")
        assert migration.returncode == 0, migration.stderr
        engine = create_async_engine(database.url)
        await _seed_refund_parents(engine)
        second_values = {
            "order_id": "order-001" if duplicate_column == "order_id" else "order-002",
            "payment_id": (
                "payment-001" if duplicate_column == "payment_id" else "payment-002"
            ),
            "fingerprint": (
                "fingerprint-001"
                if duplicate_column == "idempotency_fingerprint"
                else "fingerprint-002"
            ),
        }
        async with engine.begin() as connection:
            await connection.execute(
                text(
                    "INSERT INTO refunds "
                    "(id, order_id, payment_id, user_id, amount, status, reason, "
                    "idempotency_fingerprint, attempts, created_at, updated_at) "
                    "VALUES ('refund-001', 'order-001', 'payment-001', 'user-001', "
                    "50000, 'REQUESTED', 'customer_cancelled', 'fingerprint-001', "
                    "0, now(), now())",
                ),
            )

        # When
        with pytest.raises(IntegrityError):
            async with engine.begin() as connection:
                await connection.execute(
                    text(
                        "INSERT INTO refunds "
                        "(id, order_id, payment_id, user_id, amount, status, reason, "
                        "idempotency_fingerprint, attempts, created_at, updated_at) "
                        "VALUES ('refund-002', :order_id, :payment_id, 'user-002', "
                        "50000, 'REQUESTED', 'customer_cancelled', :fingerprint, "
                        "0, now(), now())",
                    ),
                    second_values,
                )

        # Then
        async with engine.connect() as connection:
            count = (
                await connection.execute(text("SELECT count(*) FROM refunds"))
            ).scalar_one()
        await engine.dispose()
        assert count == 1


async def _insert_terminal_payment(
    engine: AsyncEngine,
    start: anyio.Event,
    inserted_statuses: list[str],
    payment_id: str,
    payment_status: str,
) -> None:
    await start.wait()
    try:
        async with engine.begin() as connection:
            await connection.execute(
                text(
                    "INSERT INTO payments "
                    "(id, order_id, user_id, amount, method, status, idempotency_key, "
                    "created_at, approved_at, failed_at, failure_reason) VALUES "
                    "(:payment_id, 'order-terminal', 'user-001', 50000, 'MOCK_CARD', "
                    ":payment_status, :payment_id, now(), "
                    "CASE WHEN :payment_status = 'APPROVED' THEN now() ELSE NULL END, "
                    "CASE WHEN :payment_status = 'FAILED' THEN now() ELSE NULL END, "
                    "CASE WHEN :payment_status = 'FAILED' THEN 'declined' ELSE NULL END)",
                ).bindparams(
                    bindparam("payment_id", type_=String(64)),
                    bindparam("payment_status", type_=String(32)),
                ),
                {"payment_id": payment_id, "payment_status": payment_status},
            )
    except IntegrityError:
        return
    inserted_statuses.append(payment_status)


async def _seed_refund_parents(engine: AsyncEngine) -> None:
    async with engine.begin() as connection:
        await connection.execute(
            text(
                "INSERT INTO known_orders (order_id, user_id, amount, created_at) "
                "VALUES ('order-001', 'user-001', 50000, now()), "
                "('order-002', 'user-002', 50000, now())",
            ),
        )
        await connection.execute(
            text(
                "INSERT INTO payments "
                "(id, order_id, user_id, amount, method, status, idempotency_key, "
                "created_at, approved_at, failed_at, failure_reason) VALUES "
                "('payment-001', 'order-001', 'user-001', 50000, 'MOCK_CARD', "
                "'APPROVED', 'payment-key-001', now(), now(), NULL, NULL), "
                "('payment-002', 'order-002', 'user-002', 50000, 'MOCK_CARD', "
                "'APPROVED', 'payment-key-002', now(), now(), NULL, NULL)",
            ),
        )
