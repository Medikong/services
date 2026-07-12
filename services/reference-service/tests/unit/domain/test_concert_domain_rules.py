import pytest
from server.ids import deterministic_uuid_string

from app import entities as model
from app.services.serializers import public_status, seat_response


def uuid_id(*parts: object) -> str:
    return deterministic_uuid_string("concert-domain-rules-test", *parts)


@pytest.mark.parametrize(
    ("internal_status", "expected_public_status"),
    [
        ("draft", "scheduled"),
        ("submitted", "scheduled"),
        ("approved", "scheduled"),
        ("scheduled", "scheduled"),
        ("open", "open"),
        ("closed", "closed"),
        ("canceled", "canceled"),
    ],
)
def test_public_status_maps_internal_review_states_to_scheduled(internal_status: str, expected_public_status: str) -> None:
    assert public_status(internal_status) == expected_public_status


@pytest.mark.parametrize(
    ("seat_status", "expected_public_status"),
    [
        ("sellable", "available"),
        ("blocked", "locked"),
        ("hold", "locked"),
        ("reserved", "reserved"),
        ("unknown", "locked"),
    ],
)
def test_seat_response_maps_inventory_status_to_public_status(seat_status: str, expected_public_status: str) -> None:
    showtime_id = uuid_id("showtime", 1)
    seat = model.Seat(
        id=uuid_id("seat", seat_status),
        showtime_id=showtime_id,
        section="A",
        row_label="1",
        number="1",
        status=seat_status,
    )

    response = seat_response(seat)

    assert response.status == expected_public_status
    assert response.performanceId == showtime_id
    assert response.section == "A"
    assert response.row == "1"
    assert response.number == "1"
