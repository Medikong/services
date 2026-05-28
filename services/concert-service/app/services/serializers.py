from app import entities as model
from app import schemas


def page() -> schemas.PageInfo:
    return schemas.PageInfo(hasNext=False)


def venue_response(venue: model.Venue) -> schemas.VenueResponse:
    return schemas.VenueResponse(id=venue.id, name=venue.name, address=venue.address)


def draft_response(concert: model.Concert) -> schemas.ConcertDraftResponse:
    return schemas.ConcertDraftResponse(
        id=concert.id,
        providerId=concert.provider_id,
        title=concert.title,
        description=concert.description,
        posterUrl=concert.poster_url,
        ageRating=concert.age_rating,
        runningMinutes=concert.running_minutes,
        status=concert.status,
        createdAt=concert.created_at,
        updatedAt=concert.updated_at,
    )


def public_concert_response(concert: model.Concert) -> schemas.ConcertResponse:
    showtime = sorted(concert.showtimes, key=lambda item: item.starts_at)[0]
    return schemas.ConcertResponse(
        id=concert.id,
        title=concert.title,
        venue=venue_response(showtime.venue),
        startsAt=showtime.starts_at,
        status=public_status(concert.status),
    )


def showtime_response(showtime: model.Showtime) -> schemas.ShowtimeResponse:
    return schemas.ShowtimeResponse(
        id=showtime.id,
        concertId=showtime.concert_id,
        venueId=showtime.venue_id,
        startsAt=showtime.starts_at,
        endsAt=showtime.ends_at,
        status=showtime.status,
    )


def performance_response(showtime: model.Showtime) -> schemas.PerformanceResponse:
    return schemas.PerformanceResponse(
        id=showtime.id,
        concertId=showtime.concert_id,
        venueId=showtime.venue_id,
        startsAt=showtime.starts_at,
        status=public_status(showtime.status),
    )


def seat_response(seat: model.Seat) -> schemas.SeatResponse:
    status_map = {"sellable": "available", "blocked": "locked", "hold": "locked", "reserved": "reserved"}
    return schemas.SeatResponse(
        id=seat.id,
        performanceId=seat.showtime_id,
        section=seat.section,
        row=seat.row_label,
        number=seat.number,
        status=status_map.get(seat.status, "locked"),
    )


def seat_grade_response(grade: model.SeatGrade) -> schemas.SeatGradeResponse:
    return schemas.SeatGradeResponse(id=grade.id, name=grade.name, price=grade.price, color=grade.color)


def hold_request_response(request: model.HoldSeatRequest) -> schemas.HoldSeatRequestResponse:
    return schemas.HoldSeatRequestResponse(
        id=request.id,
        showtimeId=request.showtime_id,
        type=request.type,
        seatIds=request.seat_ids,
        reason=request.reason,
        status=request.status,
    )


def sale_policy_response(policy: model.SalePolicy) -> schemas.SalePolicyResponse:
    return schemas.SalePolicyResponse(
        concertId=policy.concert_id,
        presaleEnabled=policy.presale_enabled,
        fanclubVerificationRequired=policy.fanclub_verification_required,
        maxTicketsPerUser=policy.max_tickets_per_user,
        refundPolicy=policy.refund_policy,
        status=policy.status,
    )


def open_request_response(request: model.OpenRequest) -> schemas.OpenRequestResponse:
    return schemas.OpenRequestResponse(
        id=request.id,
        concertId=request.concert_id,
        requestedOpenAt=request.requested_open_at,
        status=request.status,
        message=request.message,
    )


def public_status(status: str) -> str:
    if status in {"open", "closed", "canceled"}:
        return status
    return "scheduled"
