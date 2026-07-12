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


def recommended_concert_response(concert: model.Concert) -> schemas.RecommendedConcertResponse:
    showtimes = sorted(concert.showtimes, key=lambda item: item.starts_at)
    first_showtime = showtimes[0]
    last_showtime = showtimes[-1]
    return schemas.RecommendedConcertResponse(
        concertId=concert.id,
        title=concert.title,
        posterImageUrl=concert.poster_url,
        venue=recommended_venue_summary(first_showtime.venue),
        performancePeriod=schemas.PerformancePeriodResponse(
            startDate=first_showtime.starts_at.date(),
            endDate=last_showtime.starts_at.date(),
        ),
        saleBadge=public_sale_status(concert.status),
        createdAt=concert.created_at,
    )


def concert_detail_response(concert: model.Concert) -> schemas.ConcertDetailResponse:
    showtimes = sorted(concert.showtimes, key=lambda item: item.starts_at)
    first_showtime = showtimes[0]
    last_showtime = showtimes[-1]
    close_at = last_showtime.ends_at or last_showtime.starts_at
    return schemas.ConcertDetailResponse(
        concertId=concert.id,
        title=concert.title,
        description=concert.description,
        posterImageUrl=concert.poster_url,
        venue=concert_detail_venue_response(first_showtime.venue),
        performancePeriod=schemas.PerformancePeriodResponse(
            startDate=first_showtime.starts_at.date(),
            endDate=last_showtime.starts_at.date(),
        ),
        bookingPeriod=schemas.BookingPeriodResponse(openAt=concert.opens_at or concert.created_at, closeAt=close_at),
        priceBands=price_band_responses(showtimes),
        purchaseLimit=purchase_limit_response(concert),
        notices=[
            "Check the date and performance time before booking.",
            "Seat status can change before checkout.",
        ],
        saleStatus=public_sale_status(concert.status),
    )


def recommended_venue_summary(venue: model.Venue) -> schemas.RecommendedVenueSummary:
    return schemas.RecommendedVenueSummary(venueId=venue.id, name=venue.name, city=venue.address)


def concert_detail_venue_response(venue: model.Venue) -> schemas.ConcertDetailVenueResponse:
    return schemas.ConcertDetailVenueResponse(venueId=venue.id, name=venue.name, address=venue.address, city=venue.address)


def price_band_responses(showtimes: list[model.Showtime]) -> list[schemas.PriceBandResponse]:
    bands: dict[str, schemas.PriceBandResponse] = {}
    for showtime in showtimes:
        for grade in showtime.seat_grades:
            bands.setdefault(
                grade.name,
                schemas.PriceBandResponse(
                    gradeCode=grade.name,
                    gradeName=grade.name,
                    price=grade.price,
                    currency="KRW",
                ),
            )
    return list(bands.values())


def purchase_limit_response(concert: model.Concert) -> schemas.PurchaseLimitResponse:
    max_tickets = concert.sale_policy.max_tickets_per_user if concert.sale_policy is not None else 4
    return schemas.PurchaseLimitResponse(maxTicketsPerUser=max_tickets, maxTicketsPerPerformance=max_tickets)


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


def date_performance_response(showtime: model.Showtime) -> schemas.DatePerformanceResponse:
    return schemas.DatePerformanceResponse(
        performanceId=showtime.id,
        startsAt=showtime.starts_at,
        endsAt=showtime.ends_at,
        saleStatus=public_sale_status(showtime.status),
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


def seat_map_response(showtime: model.Showtime) -> schemas.SeatMapResponse:
    grades_by_name = {grade.name: grade for grade in showtime.seat_grades}
    fallback_grade = showtime.seat_grades[0] if showtime.seat_grades else None
    sections: list[schemas.SeatMapSectionResponse] = []
    seats: list[schemas.SeatMapSeatResponse] = []
    for section_name in sorted({seat.section for seat in showtime.seats}):
        section_seats = sorted(
            [seat for seat in showtime.seats if seat.section == section_name],
            key=lambda item: (item.row_label, item.number),
        )
        grade = grades_by_name.get(section_name, fallback_grade)
        grade_code = grade.name if grade is not None else "GENERAL"
        price = grade.price if grade is not None else 0
        sections.append(
            schemas.SeatMapSectionResponse(
                sectionId=section_name,
                name=section_name,
                gradeCode=grade_code,
                price=price,
                currency="KRW",
                available=any(seat.status == "sellable" for seat in section_seats),
            )
        )
        seats.extend(
            schemas.SeatMapSeatResponse(
                seatId=seat.id,
                sectionId=seat.section,
                row=seat.row_label,
                number=seat.number,
                gradeCode=grade_code,
                status=seat_map_status(seat.status),
            )
            for seat in section_seats
        )
    return schemas.SeatMapResponse(
        performanceId=showtime.id,
        venue=schemas.SeatMapVenueResponse(venueId=showtime.venue.id, name=showtime.venue.name),
        mapVersion=showtime.starts_at.isoformat(),
        sections=sections,
        seats=seats,
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


def public_sale_status(status: str) -> str:
    status_map = {
        "closed": "CLOSED",
        "canceled": "CLOSED",
        "sold_out": "SOLD_OUT",
    }
    return status_map.get(status, "ON_SALE")


def seat_map_status(status: str) -> str:
    if status == "sellable":
        return "AVAILABLE"
    return "UNAVAILABLE"
