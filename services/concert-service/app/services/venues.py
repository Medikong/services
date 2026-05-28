from app import entities as model
from app import schemas
from app.services.base import ConcertDomainService, new_id
from app.services.serializers import page, venue_response


class VenueService(ConcertDomainService):
    def create_venue(self, request: schemas.VenueCreateRequest) -> schemas.VenueResponse:
        venue = model.Venue(id=new_id("venue"), name=request.name, address=request.address, total_seats=request.totalSeats)
        self.add(venue)
        self.commit()
        return venue_response(venue)

    def list_venues(self, limit: int) -> schemas.VenueListResponse:
        return schemas.VenueListResponse(items=[venue_response(item) for item in self.venues.list_venues(limit)], page=page())
