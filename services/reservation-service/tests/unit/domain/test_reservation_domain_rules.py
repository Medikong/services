import pytest

from app import schemas
from app.services.base import ACTIVE_STATUSES
from app.services.reservations import concert_id_from_request
from app.services.serializers import active_seat_key


def test_active_seat_key_is_stable_per_performance_and_seat() -> None:
    assert active_seat_key("perf-1", "A-10") == "perf-1:A-10"


@pytest.mark.parametrize("status", ["pending", "paid"])
def test_pending_and_paid_reservations_are_active(status: str) -> None:
    assert status in ACTIVE_STATUSES


@pytest.mark.parametrize("status", ["canceled", "expired"])
def test_canceled_and_expired_reservations_are_inactive(status: str) -> None:
    assert status not in ACTIVE_STATUSES


def test_concert_id_from_request_prefers_explicit_concert_id() -> None:
    request = schemas.CreateReservationRequest(concertId="concert-1", performanceId="perf-1", seatId="A-1")

    assert concert_id_from_request(request) == "concert-1"


def test_concert_id_from_request_derives_default_from_performance_id() -> None:
    request = schemas.CreateReservationRequest(performanceId="perf-1", seatId="A-1")

    assert concert_id_from_request(request) == "concert-perf-1"
