from app import entities as model
from app import schemas
from app.exceptions import ConflictError, NotFoundError
from app.services.base import ConcertDomainService, new_id, now_utc
from app.services.serializers import draft_response, page, public_concert_response


class ConcertCatalogService(ConcertDomainService):
    def create_concert(self, provider_id: str, request: schemas.ConcertDraftCreateRequest) -> schemas.ConcertDraftResponse:
        created_at = now_utc()
        concert = model.Concert(
            id=new_id("concert"),
            provider_id=provider_id,
            title=request.title,
            description=request.description,
            poster_url=request.posterUrl,
            age_rating=request.ageRating,
            running_minutes=request.runningMinutes,
            status="draft",
            created_at=created_at,
        )
        self.add(concert)
        self.add(
            model.ConcertReviewRequest(
                id=new_id("review"),
                concert_id=concert.id,
                provider_id=provider_id,
                type="concert",
                status="pending",
                submitted_at=created_at,
            )
        )
        self.commit()
        return draft_response(concert)

    def update_concert(self, concert_id: str, request: schemas.ConcertUpdateRequest) -> schemas.ConcertDraftResponse:
        concert = self._concert(concert_id)
        values = request.model_dump(exclude_unset=True)
        if not values:
            raise ConflictError("concert.empty_update", "At least one field must be supplied.")
        if "title" in values:
            concert.title = values["title"]
        if "description" in values:
            concert.description = values["description"]
        if "posterUrl" in values:
            concert.poster_url = values["posterUrl"]
        if "ageRating" in values:
            concert.age_rating = values["ageRating"]
        if "runningMinutes" in values:
            concert.running_minutes = values["runningMinutes"]
        concert.updated_at = now_utc()
        self.commit()
        return draft_response(concert)

    def list_public_concerts(self, limit: int) -> schemas.ConcertListResponse:
        items = [public_concert_response(concert) for concert in self.concerts.list_concerts(limit) if concert.showtimes]
        return schemas.ConcertListResponse(items=items, page=page())

    def get_public_concert(self, concert_id: str) -> schemas.ConcertResponse:
        concert = self._concert(concert_id)
        if not concert.showtimes:
            raise NotFoundError("concert", concert_id)
        return public_concert_response(concert)
