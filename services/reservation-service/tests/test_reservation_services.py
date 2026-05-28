import pytest
from sqlalchemy import create_engine
from sqlalchemy.orm import Session, sessionmaker

from app import schemas
from app.database import Base
from app.exceptions import ConflictError
from app.services import ReservationCommandService, ReservationPolicyService, ReservationQueryService, SalesService


@pytest.fixture()
def db_session() -> Session:
    engine = create_engine("sqlite:///:memory:", connect_args={"check_same_thread": False})
    Base.metadata.create_all(engine)
    session_factory = sessionmaker(bind=engine)
    session = session_factory()
    try:
        yield session
    finally:
        session.close()
        Base.metadata.drop_all(engine)


def test_reservation_state_transitions_and_duplicate_conflict(db_session: Session) -> None:
    command_service = ReservationCommandService(db_session)
    request = schemas.CreateReservationRequest(
        concertId="concert-service-flow",
        showtimeId="showtime-service-flow",
        performanceId="perf-service-flow",
        seatId="A-1",
    )
    reservation = command_service.create_reservation("user-service", request)

    with pytest.raises(ConflictError):
        command_service.create_reservation("user-service-2", request)

    canceled = command_service.cancel_reservation(reservation.id)

    assert canceled.status == "canceled"


def test_sales_state_transitions_and_policies(db_session: Session) -> None:
    sales_service = SalesService(db_session)
    policy_service = ReservationPolicyService(db_session)

    assert sales_service.start_sales("concert-policy").salesStatus == "open"
    assert sales_service.pause_sales("concert-policy").salesStatus == "paused"
    assert sales_service.resume_sales("concert-policy").salesStatus == "open"
    assert policy_service.update_queue_policy(
        "concert-policy",
        schemas.QueuePolicyUpdateRequest(enabled=True, maxEntrantsPerMinute=50),
    ).enabled is True


def test_query_service_returns_user_reservations(db_session: Session) -> None:
    ReservationCommandService(db_session).create_reservation(
        "user-query",
        schemas.CreateReservationRequest(concertId="concert-query", performanceId="perf-query", seatId="A-1"),
    )

    reservations = ReservationQueryService(db_session).list_my_reservations("user-query", 20)

    assert len(reservations.items) == 1
