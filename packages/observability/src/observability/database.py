from __future__ import annotations

from typing import Any


_sqlalchemy_instrumented_engine_ids: set[int] = set()
_sqlalchemy_pool_event_engine_ids: set[int] = set()
_sqlalchemy_instrumented = False
_pymongo_instrumented = False


def instrument_sqlalchemy() -> None:
    global _sqlalchemy_instrumented

    if _sqlalchemy_instrumented:
        return

    from opentelemetry.instrumentation.sqlalchemy import SQLAlchemyInstrumentor

    SQLAlchemyInstrumentor().instrument()
    _sqlalchemy_instrumented = True


def instrument_sqlalchemy_engine(engine: Any) -> None:
    if engine is None:
        raise ValueError("engine must not be None")

    engine_id = id(engine)
    if engine_id in _sqlalchemy_instrumented_engine_ids:
        return

    from opentelemetry.instrumentation.sqlalchemy import SQLAlchemyInstrumentor

    SQLAlchemyInstrumentor().instrument(engine=engine)
    _sqlalchemy_instrumented_engine_ids.add(engine_id)


def instrument_sqlalchemy_pool_events(engine: Any) -> None:
    if engine is None:
        raise ValueError("engine must not be None")

    engine_id = id(engine)
    if engine_id in _sqlalchemy_pool_event_engine_ids:
        return

    from sqlalchemy import event

    def record_pool_checkout(dbapi_connection: Any, connection_record: Any, connection_proxy: Any) -> None:
        del dbapi_connection, connection_record, connection_proxy
        _record_pool_event("sqlalchemy.pool.checkout", engine)

    def record_pool_checkin(dbapi_connection: Any, connection_record: Any) -> None:
        del dbapi_connection, connection_record
        _record_pool_event("sqlalchemy.pool.checkin", engine)

    event.listen(engine, "checkout", record_pool_checkout)
    event.listen(engine, "checkin", record_pool_checkin)
    _sqlalchemy_pool_event_engine_ids.add(engine_id)


def _record_pool_event(name: str, engine: Any) -> None:
    from observability.tracing import trace_recorder

    recorder = trace_recorder()
    recorder.event(
        name,
        {
            "db.system": "sqlalchemy",
            "db.pool.status": engine.pool.status(),
        },
    )


def instrument_motor_client(client: Any) -> None:
    global _pymongo_instrumented

    if client is None:
        raise ValueError("client must not be None")

    if _pymongo_instrumented:
        return

    from opentelemetry.instrumentation.pymongo import PymongoInstrumentor

    PymongoInstrumentor().instrument()
    _pymongo_instrumented = True
