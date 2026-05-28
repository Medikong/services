from datetime import UTC, datetime, timedelta

import pytest
from sqlalchemy import create_engine
from sqlalchemy.orm import Session, sessionmaker

from app import schemas
from app.database import Base
from app.services import ConcertCatalogService, SeatService, ShowtimeService, VenueService


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


def test_service_layers_create_concert_showtime_and_seats(db_session: Session) -> None:
    venue = VenueService(db_session).create_venue(schemas.VenueCreateRequest(name="Hall"))
    concert = ConcertCatalogService(db_session).create_concert(
        "provider-service",
        schemas.ConcertDraftCreateRequest(title="Layered Live", ageRating="ALL", runningMinutes=100),
    )
    showtime = ShowtimeService(db_session).create_showtime(
        concert.id,
        schemas.ShowtimeCreateRequest(venueId=venue.id, startsAt=datetime.now(UTC) + timedelta(days=1)),
    )

    SeatService(db_session).upload_seat_map(
        showtime.id,
        schemas.SeatMapRequest(sections=[schemas.SeatSectionRequest(name="A", rows=[schemas.SeatRowRequest(name="1", seatNumbers=["1"])])]),
    )
    seats = SeatService(db_session).list_seats(showtime.id, 20)

    assert seats.items[0].status == "available"


def test_duplicate_seat_grade_is_conflict(db_session: Session) -> None:
    venue = VenueService(db_session).create_venue(schemas.VenueCreateRequest(name="Grade Hall"))
    concert = ConcertCatalogService(db_session).create_concert(
        "provider-service",
        schemas.ConcertDraftCreateRequest(title="Grade Live", ageRating="ALL", runningMinutes=100),
    )
    showtime = ShowtimeService(db_session).create_showtime(
        concert.id,
        schemas.ShowtimeCreateRequest(venueId=venue.id, startsAt=datetime.now(UTC) + timedelta(days=1)),
    )
    request = schemas.SeatGradeCreateRequest(grades=[schemas.SeatGradeResponse(id="vip", name="VIP", price=100000)])

    SeatService(db_session).create_seat_grades(showtime.id, request)

    with pytest.raises(Exception, match="Seat grade already exists"):
        SeatService(db_session).create_seat_grades(showtime.id, request)
