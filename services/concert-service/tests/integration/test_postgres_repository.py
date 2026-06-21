from collections.abc import Iterator
from itertools import count
from datetime import UTC, datetime, timedelta

import pytest
from sqlalchemy import create_engine
from sqlalchemy.engine import Engine
from sqlalchemy.orm import Session, sessionmaker

from app import schemas
from app.database import Base
from app.exceptions import SeatGradeAlreadyExistsError, SeatMapContainsDuplicateSeatsError
from app.repositories import ConcertReviewRepository, SeatRepository, ShowtimeRepository
from app.services import ConcertCatalogService, ConcertReviewService, SeatService, ShowtimeService, VenueService


_suffixes = count(1)


def _suffix() -> str:
    return f"pg-{next(_suffixes):04d}"


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


def _create_showtime(session: Session, suffix: str, starts_at: datetime | None = None):
    venue = VenueService(session).create_venue(schemas.VenueCreateRequest(name=f"Postgres Hall {suffix}"))
    concert = ConcertCatalogService(session).create_concert(
        f"provider-{suffix}",
        schemas.ConcertDraftCreateRequest(title=f"Postgres Live {suffix}", ageRating="ALL", runningMinutes=80),
    )
    return ShowtimeService(session).create_showtime(
        concert.id,
        schemas.ShowtimeCreateRequest(venueId=venue.id, startsAt=starts_at or datetime.now(UTC) + timedelta(days=1)),
    )


def test_postgres_enforces_unique_seat_grade_name(db_session: Session) -> None:
    showtime = _create_showtime(db_session, _suffix())
    service = SeatService(db_session)

    service.create_seat_grades(
        showtime.id,
        schemas.SeatGradeCreateRequest(grades=[schemas.SeatGradeResponse(id="pg-vip-1", name="VIP", price=100000)]),
    )

    with pytest.raises(SeatGradeAlreadyExistsError, match="Seat grade already exists"):
        service.create_seat_grades(
            showtime.id,
            schemas.SeatGradeCreateRequest(grades=[schemas.SeatGradeResponse(id="pg-vip-2", name="VIP", price=120000)]),
        )


def test_postgres_rolls_back_duplicate_seat_map_locations(db_session: Session) -> None:
    showtime = _create_showtime(db_session, _suffix())
    request = schemas.SeatMapRequest(
        sections=[schemas.SeatSectionRequest(name="A", rows=[schemas.SeatRowRequest(name="1", seatNumbers=["1", "1"])])]
    )

    with pytest.raises(SeatMapContainsDuplicateSeatsError, match="Seat map contains duplicate seats"):
        SeatService(db_session).upload_seat_map(showtime.id, request)

    assert list(SeatRepository(db_session).list_seats(showtime.id, limit=20)) == []


def test_postgres_persists_review_request_query_and_status(db_session: Session) -> None:
    suffix = _suffix()
    concert = ConcertCatalogService(db_session).create_concert(
        f"provider-review-{suffix}",
        schemas.ConcertDraftCreateRequest(title=f"Review Live {suffix}", ageRating="ALL", runningMinutes=70),
    )

    requests = ConcertReviewService(db_session).list_review_requests(limit=10)
    request_id = requests.items[0].id
    fetched = ConcertReviewService(db_session).get_review_request(request_id)
    approved = ConcertReviewService(db_session).approve_review_request(request_id)
    stored = ConcertReviewRepository(db_session).get_review_request(request_id)

    assert fetched.concertId == concert.id
    assert approved.status == "approved"
    assert stored is not None
    assert stored.status == "approved"
    assert stored.reviewed_at is not None


def test_postgres_catalog_read_models_match_new_public_api(db_session: Session) -> None:
    starts_at = datetime(2026, 7, 18, 14, 0, tzinfo=UTC)
    showtime = _create_showtime(db_session, _suffix(), starts_at=starts_at)
    SeatService(db_session).upload_seat_map(
        showtime.id,
        schemas.SeatMapRequest(
            sections=[schemas.SeatSectionRequest(name="A", rows=[schemas.SeatRowRequest(name="1", seatNumbers=["1", "2"])])]
        ),
    )
    SeatService(db_session).create_seat_grades(
        showtime.id,
        schemas.SeatGradeCreateRequest(grades=[schemas.SeatGradeResponse(id="pg-vip", name="A", price=100000)]),
    )

    catalog = ConcertCatalogService(db_session)
    performances = ShowtimeService(db_session).list_performances_by_date(showtime.concertId, starts_at.date())
    calendar = catalog.get_monthly_availability(showtime.concertId, "2026-07")
    seat_map = SeatService(db_session).get_seat_map(showtime.id)

    assert next(day for day in calendar.days if day.date == starts_at.date()).bookable is True
    assert [item.performanceId for item in performances.performances] == [showtime.id]
    assert seat_map.sections[0].sectionId == "A"
    assert seat_map.sections[0].price == 100000
    assert [seat.status for seat in seat_map.seats] == ["AVAILABLE", "AVAILABLE"]


def test_postgres_calendar_bookable_dates_use_sellable_seat_exists(db_session: Session) -> None:
    sellable_starts_at = datetime(2026, 7, 18, 14, 0, tzinfo=UTC)
    blocked_starts_at = datetime(2026, 7, 19, 14, 0, tzinfo=UTC)
    closed_starts_at = datetime(2026, 7, 20, 14, 0, tzinfo=UTC)
    suffix = _suffix()
    venue = VenueService(db_session).create_venue(schemas.VenueCreateRequest(name=f"Calendar Hall {suffix}"))
    concert = ConcertCatalogService(db_session).create_concert(
        f"provider-calendar-{suffix}",
        schemas.ConcertDraftCreateRequest(title=f"Calendar Live {suffix}", ageRating="ALL", runningMinutes=70),
    )
    sellable_showtime = ShowtimeService(db_session).create_showtime(
        concert.id,
        schemas.ShowtimeCreateRequest(venueId=venue.id, startsAt=sellable_starts_at),
    )
    blocked_showtime = ShowtimeService(db_session).create_showtime(
        concert.id,
        schemas.ShowtimeCreateRequest(venueId=venue.id, startsAt=blocked_starts_at),
    )
    closed_showtime = ShowtimeService(db_session).create_showtime(
        concert.id,
        schemas.ShowtimeCreateRequest(venueId=venue.id, startsAt=closed_starts_at),
    )
    SeatService(db_session).upload_seat_map(
        sellable_showtime.id,
        schemas.SeatMapRequest(
            sections=[schemas.SeatSectionRequest(name="A", rows=[schemas.SeatRowRequest(name="1", seatNumbers=["1"])])]
        ),
    )
    SeatService(db_session).upload_seat_map(
        blocked_showtime.id,
        schemas.SeatMapRequest(
            sections=[schemas.SeatSectionRequest(name="A", rows=[schemas.SeatRowRequest(name="1", seatNumbers=["1"])])]
        ),
    )
    SeatService(db_session).upload_seat_map(
        closed_showtime.id,
        schemas.SeatMapRequest(
            sections=[schemas.SeatSectionRequest(name="A", rows=[schemas.SeatRowRequest(name="1", seatNumbers=["1"])])]
        ),
    )
    blocked_seat = next(iter(SeatRepository(db_session).list_seats(blocked_showtime.id, limit=1)))
    SeatService(db_session).update_seat_inventory(
        blocked_showtime.id,
        schemas.SeatInventoryUpdateRequest(
            seats=[schemas.SeatInventoryItem(seatId=blocked_seat.id, status="blocked")]
        ),
    )
    closed_showtime_entity = ShowtimeRepository(db_session).get_showtime(closed_showtime.id)
    assert closed_showtime_entity is not None
    closed_showtime_entity.status = "closed"
    db_session.commit()

    calendar = ConcertCatalogService(db_session).get_monthly_availability(concert.id, "2026-07")

    assert next(day for day in calendar.days if day.date == sellable_starts_at.date()).bookable is True
    assert next(day for day in calendar.days if day.date == blocked_starts_at.date()).bookable is False
    assert next(day for day in calendar.days if day.date == closed_starts_at.date()).bookable is False
