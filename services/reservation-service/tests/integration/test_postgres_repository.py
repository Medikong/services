from collections.abc import Iterator
from concurrent.futures import ThreadPoolExecutor
from datetime import UTC, datetime, timedelta
from uuid import uuid4

import pytest
from sqlalchemy import create_engine, func, select
from sqlalchemy.engine import Engine
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session, sessionmaker

from app import entities as model
from app import schemas
from app.database import Base
from app.exceptions import SeatAlreadyReservedError
from app.repositories import ReservationRepository
from app.services import ReservationCommandService
from app.services.serializers import active_seat_key


@pytest.fixture(scope="module")
def postgres_engine() -> Iterator[Engine]:
    postgres = pytest.importorskip("testcontainers.postgres")
    docker = pytest.importorskip("docker")
    try:
        docker.from_env().ping()
    except Exception as exc:
        pytest.skip(f"Docker is not available for Testcontainers: {exc}")

    with postgres.PostgresContainer("postgres:16-alpine") as container:
        engine = create_engine(container.get_connection_url(driver="psycopg"))
        try:
            yield engine
        finally:
            engine.dispose()


@pytest.fixture()
def db_session(postgres_engine: Engine) -> Iterator[Session]:
    Base.metadata.drop_all(postgres_engine)
    Base.metadata.create_all(postgres_engine)
    session = sessionmaker(bind=postgres_engine)()
    try:
        yield session
    finally:
        session.close()
        Base.metadata.drop_all(postgres_engine)


def _request(suffix: str, seat_id: str = "A-1") -> schemas.CreateReservationRequest:
    return schemas.CreateReservationRequest(
        concertId=f"concert-{suffix}",
        showtimeId=f"showtime-{suffix}",
        performanceId=f"perf-{suffix}",
        seatId=seat_id,
    )


def test_postgres_active_reservation_query_and_conflict(db_session: Session) -> None:
    """Postgres에서 활성 예약 조회와 중복 좌석 예약 충돌 처리를 검증한다."""
    suffix = uuid4().hex[:8]
    request = _request(suffix)
    service = ReservationCommandService(db_session)
    created = service.create_reservation("user-pg-1", request)

    active = ReservationRepository(db_session).find_active_reservation(request.performanceId, request.seatId)

    assert active is not None
    assert active.id == created.id
    with pytest.raises(SeatAlreadyReservedError, match="Seat is already reserved"):
        service.create_reservation("user-pg-2", request)


def test_postgres_unique_active_seat_key_constraint(db_session: Session) -> None:
    """같은 활성 좌석 키가 두 번 저장될 때 DB 유니크 제약이 막는지 검증한다."""
    suffix = uuid4().hex[:8]
    key = active_seat_key(f"perf-{suffix}", "A-1")
    expires_at = datetime.now(UTC) + timedelta(minutes=5)
    created_at = datetime.now(UTC)

    db_session.add_all(
        [
            model.Reservation(
                id=f"rsv-{suffix}-1",
                user_id="user-1",
                concert_id=f"concert-{suffix}",
                showtime_id=f"showtime-{suffix}",
                performance_id=f"perf-{suffix}",
                seat_id="A-1",
                status="pending",
                active_seat_key=key,
                expires_at=expires_at,
                created_at=created_at,
            ),
            model.Reservation(
                id=f"rsv-{suffix}-2",
                user_id="user-2",
                concert_id=f"concert-{suffix}",
                showtime_id=f"showtime-{suffix}",
                performance_id=f"perf-{suffix}",
                seat_id="A-1",
                status="pending",
                active_seat_key=key,
                expires_at=expires_at,
                created_at=created_at,
            ),
        ]
    )

    with pytest.raises(IntegrityError):
        db_session.commit()
    db_session.rollback()

    assert ReservationRepository(db_session).find_active_reservation(f"perf-{suffix}", "A-1") is None


def test_postgres_allows_re_reservation_after_cancel_or_expire(db_session: Session) -> None:
    """취소 또는 만료된 예약의 좌석을 다시 예약할 수 있는지 검증한다."""
    service = ReservationCommandService(db_session)
    cancel_request = _request(f"cancel-{uuid4().hex[:8]}")
    expire_request = _request(f"expire-{uuid4().hex[:8]}")

    canceled = service.cancel_reservation(service.create_reservation("user-cancel-1", cancel_request).id)
    after_cancel = service.create_reservation("user-cancel-2", cancel_request)
    expired = service.expire_reservation(service.create_reservation("user-expire-1", expire_request).id)
    after_expire = service.create_reservation("user-expire-2", expire_request)

    assert canceled.status == "canceled"
    assert after_cancel.status == "pending"
    assert expired.status == "expired"
    assert after_expire.status == "pending"


def test_postgres_concurrent_reservations_allow_only_one_active_hold(db_session: Session) -> None:
    """동시에 같은 좌석을 예약해도 활성 예약은 하나만 남는지 검증한다."""
    suffix = uuid4().hex[:8]
    request = _request(f"concurrent-{suffix}")
    session_factory = sessionmaker(bind=db_session.get_bind())
    attempts = 12

    def create_hold(index: int) -> str:
        session = session_factory()
        try:
            ReservationCommandService(session).create_reservation(f"user-concurrent-{index}", request)
            return "created"
        except SeatAlreadyReservedError:
            return "conflict"
        finally:
            session.close()

    with ThreadPoolExecutor(max_workers=attempts) as executor:
        results = list(executor.map(create_hold, range(attempts)))

    active_count = db_session.scalar(
        select(func.count())
        .select_from(model.Reservation)
        .where(
            model.Reservation.performance_id == request.performanceId,
            model.Reservation.seat_id == request.seatId,
            model.Reservation.status.in_(("pending", "paid")),
        )
    )

    assert results.count("created") == 1
    assert results.count("conflict") == attempts - 1
    assert active_count == 1
