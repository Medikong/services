import pytest

from app import schemas
from app.services.base import ACTIVE_STATUSES
from app.services.reservations import concert_id_from_request
from app.services.serializers import active_seat_key


def test_active_seat_key_is_stable_per_performance_and_seat() -> None:
    """활성 좌석 키가 회차와 좌석 조합으로 안정적으로 만들어지는지 검증한다."""
    assert active_seat_key("perf-1", "A-10") == "perf-1:A-10"


@pytest.mark.parametrize("status", ["pending", "paid"])
def test_pending_and_paid_reservations_are_active(status: str) -> None:
    """대기 및 결제 완료 예약 상태를 활성 상태로 판단하는지 검증한다."""
    assert status in ACTIVE_STATUSES


@pytest.mark.parametrize("status", ["canceled", "expired"])
def test_canceled_and_expired_reservations_are_inactive(status: str) -> None:
    """취소 및 만료 예약 상태를 비활성 상태로 판단하는지 검증한다."""
    assert status not in ACTIVE_STATUSES


def test_concert_id_from_request_prefers_explicit_concert_id() -> None:
    """요청에 concertId가 있으면 해당 값을 우선 사용하는지 검증한다."""
    request = schemas.CreateReservationRequest(concertId="concert-1", performanceId="perf-1", seatId="A-1")

    assert concert_id_from_request(request) == "concert-1"


def test_concert_id_from_request_derives_default_from_performance_id() -> None:
    """요청에 concertId가 없으면 performanceId에서 기본 concertId를 만드는지 검증한다."""
    request = schemas.CreateReservationRequest(performanceId="perf-1", seatId="A-1")

    assert concert_id_from_request(request) == "concert-perf-1"
