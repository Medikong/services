from collections.abc import Generator
import time

from sqlalchemy.exc import OperationalError
from sqlalchemy import create_engine
from sqlalchemy.orm import DeclarativeBase, Session, sessionmaker
from observability import instrument_sqlalchemy_engine, instrument_sqlalchemy_pool_events, trace_recorder
from server import sqlalchemy_engine_options_from_env

from app.config import settings


engine = create_engine(settings.database_url, **sqlalchemy_engine_options_from_env(settings.database_url))
instrument_sqlalchemy_engine(engine)
instrument_sqlalchemy_pool_events(engine)
SessionLocal = sessionmaker(autocommit=False, autoflush=False, bind=engine)


class Base(DeclarativeBase):
    pass


def get_db() -> Generator[Session, None, None]:
    recorder = trace_recorder()
    with recorder.span("concert.dependency.db.session_create"):
        db = SessionLocal()
    try:
        yield db
    finally:
        with recorder.span("concert.dependency.db.session_close"):
            db.close()


def init_db() -> None:
    from app import entities  # noqa: F401

    last_error: OperationalError | None = None
    for _ in range(10):
        try:
            Base.metadata.create_all(bind=engine)
            return
        except OperationalError as exc:
            last_error = exc
            time.sleep(1)
    if last_error is not None:
        raise last_error
