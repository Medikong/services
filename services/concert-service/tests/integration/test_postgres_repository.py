from collections.abc import Iterator
from datetime import UTC, datetime, timedelta
from uuid import uuid4

import pytest
from sqlalchemy import create_engine
from sqlalchemy.engine import Engine
from sqlalchemy.orm import Session, sessionmaker

from app import schemas
from app.database import Base
from app.exceptions import ConflictError
from app.repositories import ConcertReviewRepository, SeatRepository
from app.services import ConcertCatalogService, ConcertReviewService, SeatService, ShowtimeService, VenueService


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


def _create_showtime(session: Session, suffix: str):
    venue = VenueService(session).create_venue(schemas.VenueCreateRequest(name=f"Postgres Hall {suffix}"))
    concert = ConcertCatalogService(session).create_concert(
        f"provider-{suffix}",
        schemas.ConcertDraftCreateRequest(title=f"Postgres Live {suffix}", ageRating="ALL", runningMinutes=80),
    )
    return ShowtimeService(session).create_showtime(
        concert.id,
        schemas.ShowtimeCreateRequest(venueId=venue.id, startsAt=datetime.now(UTC) + timedelta(days=1)),
    )


def test_postgres_enforces_unique_seat_grade_name(db_session: Session) -> None:
    showtime = _create_showtime(db_session, uuid4().hex[:8])
    service = SeatService(db_session)

    service.create_seat_grades(
        showtime.id,
        schemas.SeatGradeCreateRequest(grades=[schemas.SeatGradeResponse(id="pg-vip-1", name="VIP", price=100000)]),
    )

    with pytest.raises(ConflictError, match="Seat grade already exists"):
        service.create_seat_grades(
            showtime.id,
            schemas.SeatGradeCreateRequest(grades=[schemas.SeatGradeResponse(id="pg-vip-2", name="VIP", price=120000)]),
        )


def test_postgres_rolls_back_duplicate_seat_map_locations(db_session: Session) -> None:
    showtime = _create_showtime(db_session, uuid4().hex[:8])
    request = schemas.SeatMapRequest(
        sections=[schemas.SeatSectionRequest(name="A", rows=[schemas.SeatRowRequest(name="1", seatNumbers=["1", "1"])])]
    )

    with pytest.raises(ConflictError, match="Seat map contains duplicate seats"):
        SeatService(db_session).upload_seat_map(showtime.id, request)

    assert list(SeatRepository(db_session).list_seats(showtime.id, limit=20)) == []


def test_postgres_persists_review_request_query_and_status(db_session: Session) -> None:
    suffix = uuid4().hex[:8]
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
