from collections.abc import Iterator
from contextlib import contextmanager

import pytest
from sqlalchemy import create_engine
from sqlalchemy.orm import Session, sessionmaker
from server.ids import deterministic_uuid_string

from app import schemas
from app.database import Base
from app.exceptions import SeatAlreadyReservedError
from app.services import ReservationCommandService, ReservationPolicyService, ReservationQueryService, SalesService


def uuid_id(*parts: object) -> str:
    return deterministic_uuid_string("reservation-service-test", *parts)


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
    """예약 생성, 중복 좌석 충돌, 취소 상태 전이를 검증한다."""
    command_service = ReservationCommandService(db_session)
    concert_id = uuid_id("concert", "service-flow")
    showtime_id = uuid_id("showtime", "service-flow")
    performance_id = uuid_id("performance", "service-flow")
    seat_id = uuid_id("seat", "service-flow")
    request = schemas.CreateReservationRequest(
        concertId=concert_id,
        showtimeId=showtime_id,
        performanceId=performance_id,
        seatId=seat_id,
    )
    reservation = command_service.create_reservation("user-service", request)

    with pytest.raises(SeatAlreadyReservedError):
        command_service.create_reservation("user-service-2", request)

    canceled = command_service.cancel_reservation(reservation.id)

    assert canceled.status == "canceled"


def test_ticketed_reservation_blocks_rebooking_same_seat(db_session: Session) -> None:
    command_service = ReservationCommandService(db_session)
    concert_id = uuid_id("concert", "ticketed-lock")
    showtime_id = uuid_id("showtime", "ticketed-lock")
    performance_id = uuid_id("performance", "ticketed-lock")
    seat_id = uuid_id("seat", "ticketed-lock")
    request = schemas.CreateReservationRequest(
        concertId=concert_id,
        showtimeId=showtime_id,
        performanceId=performance_id,
        seatId=seat_id,
    )
    reservation = command_service.create_reservation("user-ticketed-1", request)

    command_service.confirm_reservation(reservation.id)

    with pytest.raises(SeatAlreadyReservedError):
        command_service.create_reservation("user-ticketed-2", request)


def test_create_reservation_records_manual_trace(db_session: Session) -> None:
    trace = RecordingTraceRecorder()
    command_service = ReservationCommandService(db_session, trace=trace)
    concert_id = uuid_id("concert", "trace")
    showtime_id = uuid_id("showtime", "trace")
    performance_id = uuid_id("performance", "trace")
    seat_id = uuid_id("seat", "trace")
    request = schemas.CreateReservationRequest(
        concertId=concert_id,
        showtimeId=showtime_id,
        performanceId=performance_id,
        seatId=seat_id,
    )

    reservation = command_service.create_reservation("user-trace", request)

    assert ("app.use_case", "reserve_seat") in trace.attributes
    assert ("concert.id", concert_id) in trace.attributes
    assert ("performance.id", performance_id) in trace.attributes
    assert ("seat.id", seat_id) in trace.attributes
    assert ("reservation.id", reservation.id) in trace.attributes
    assert trace.events == [("seat.hold.created", {"reservation.id": reservation.id, "seat.id": seat_id})]
    assert trace.spans == ["reservation.reserve_seat"]


def test_sales_state_transitions_and_policies(db_session: Session) -> None:
    """판매 상태 전이와 예약 정책 갱신 흐름을 검증한다."""
    sales_service = SalesService(db_session)
    policy_service = ReservationPolicyService(db_session)
    concert_id = uuid_id("concert", "policy")

    assert sales_service.start_sales(concert_id).salesStatus == "open"
    assert sales_service.pause_sales(concert_id).salesStatus == "paused"
    assert sales_service.resume_sales(concert_id).salesStatus == "open"
    assert policy_service.update_queue_policy(
        concert_id,
        schemas.QueuePolicyUpdateRequest(enabled=True, maxEntrantsPerMinute=50),
    ).enabled is True


def test_query_service_returns_user_reservations(db_session: Session) -> None:
    """사용자 예약 목록 조회가 생성된 예약을 반환하는지 검증한다."""
    ReservationCommandService(db_session).create_reservation(
        "user-query",
        schemas.CreateReservationRequest(
            concertId=uuid_id("concert", "query"),
            performanceId=uuid_id("performance", "query"),
            seatId=uuid_id("seat", "query"),
        ),
    )

    reservations = ReservationQueryService(db_session).list_my_reservations("user-query", 20)

    assert len(reservations.items) == 1


class RecordingTraceRecorder:
    def __init__(self) -> None:
        self.attributes: list[tuple[str, object]] = []
        self.events: list[tuple[str, dict[str, object] | None]] = []
        self.spans: list[str] = []

    def attribute(self, key: str, value: object) -> None:
        self.attributes.append((key, value))

    def event(self, name: str, attributes: dict[str, object] | None = None) -> None:
        self.events.append((name, attributes))

    def span(self, name: str, attributes: dict[str, object] | None = None):
        self.spans.append(name)

        @contextmanager
        def child_span() -> Iterator[None]:
            yield

        return child_span()
