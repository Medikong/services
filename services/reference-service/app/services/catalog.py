import base64
import json
import re
from calendar import monthrange
from datetime import UTC, datetime

from metrics import MetricResult
from observability import DOMAIN_REJECTION_OBSERVATION, HttpError

from app import entities as model
from app import schemas
from app.exceptions import ConcertEmptyUpdateError, InvalidPublicRequestError, PublicConcertNotFoundError
from app.metrics.events import ConcertAdminCommandRecorded
from app.metrics.labels import CatalogResource, ConcertAdminCommand
from app.metrics.recorder import ConcertTelemetryRecorder
from app.services.base import ConcertDomainService, new_id, now_utc
from app.services.serializers import (
    concert_detail_response,
    draft_response,
    page,
    public_concert_response,
    recommended_concert_response,
)


concert_metrics = ConcertTelemetryRecorder()
RECOMMENDED_DEFAULT_LIMIT = 10
RECOMMENDED_MAX_LIMIT = 12
SERVICE_TIMEZONE = "Asia/Seoul"


class ConcertCatalogService(ConcertDomainService):
    def create_concert(self, provider_id: str, request: schemas.ConcertDraftCreateRequest) -> schemas.ConcertDraftResponse:
        """공연 생성 command 결과를 metric으로 남긴다."""
        try:
            created_at = now_utc()
            concert = model.Concert(
                id=new_id(),
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
                    id=new_id(),
                    concert_id=concert.id,
                    provider_id=provider_id,
                    type="concert",
                    status="pending",
                    submitted_at=created_at,
                )
            )
            self.commit()
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            concert_metrics.record(
                ConcertAdminCommandRecorded(command=ConcertAdminCommand.CREATE_CONCERT, result=MetricResult.REJECTION)
            )
            raise
        except Exception:
            concert_metrics.record(
                ConcertAdminCommandRecorded(command=ConcertAdminCommand.CREATE_CONCERT, result=MetricResult.FAILURE)
            )
            raise
        concert_metrics.record(
            ConcertAdminCommandRecorded(command=ConcertAdminCommand.CREATE_CONCERT, result=MetricResult.SUCCESS)
        )
        return draft_response(concert)

    def update_concert(self, concert_id: str, request: schemas.ConcertUpdateRequest) -> schemas.ConcertDraftResponse:
        """공연 수정 command 결과를 metric으로 남긴다."""
        try:
            concert = self._concert(concert_id)
            values = request.model_dump(exclude_unset=True)
            if not values:
                raise ConcertEmptyUpdateError()
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
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            concert_metrics.record(
                ConcertAdminCommandRecorded(command=ConcertAdminCommand.UPDATE_CONCERT, result=MetricResult.REJECTION)
            )
            raise
        except Exception:
            concert_metrics.record(
                ConcertAdminCommandRecorded(command=ConcertAdminCommand.UPDATE_CONCERT, result=MetricResult.FAILURE)
            )
            raise
        concert_metrics.record(
            ConcertAdminCommandRecorded(command=ConcertAdminCommand.UPDATE_CONCERT, result=MetricResult.SUCCESS)
        )
        return draft_response(concert)

    def list_public_concerts(self, limit: int) -> schemas.ConcertListResponse:
        """공연 목록 조회 처리 시간과 결과를 metric으로 남긴다."""
        attempt = concert_metrics.start_catalog_query(CatalogResource.CONCERTS)
        try:
            items = [public_concert_response(concert) for concert in self.concerts.list_concerts(min(limit, RECOMMENDED_MAX_LIMIT)) if concert.showtimes]
            response = schemas.ConcertListResponse(items=items, page=page())
            attempt.mark_success()
            return response
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            attempt.mark_rejection()
            raise
        finally:
            attempt.record()

    def get_public_concert(self, concert_id: str) -> schemas.ConcertResponse:
        """공연 상세 조회 처리 시간과 결과를 metric으로 남긴다."""
        attempt = concert_metrics.start_catalog_query(CatalogResource.CONCERT)
        try:
            concert = self._concert(concert_id)
            if not concert.showtimes:
                raise PublicConcertNotFoundError(concert_id)
            response = public_concert_response(concert)
            attempt.mark_success()
            return response
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            attempt.mark_rejection()
            raise
        finally:
            attempt.record()

    def list_recommended_concerts(
        self,
        sort: str = "latest",
        cursor: str | None = None,
        limit: int = RECOMMENDED_DEFAULT_LIMIT,
    ) -> schemas.RecommendedConcertListResponse:
        """모바일 추천 피드용 공연 카드 목록을 작은 page 단위로 반환한다."""
        attempt = concert_metrics.start_catalog_query(CatalogResource.CONCERTS)
        try:
            if sort != "latest":
                raise InvalidPublicRequestError("sort must be latest.")
            applied_limit = min(limit, RECOMMENDED_MAX_LIMIT)
            before = decode_recommended_cursor(cursor)
            concerts = [concert for concert in self.concerts.list_recommended_concerts(applied_limit + 1, before) if concert.showtimes]
            page_items = concerts[:applied_limit]
            next_cursor = encode_recommended_cursor(page_items[-1].created_at) if len(concerts) > applied_limit and page_items else None
            response = schemas.RecommendedConcertListResponse(
                items=[recommended_concert_response(concert) for concert in page_items],
                page=schemas.CursorPageInfo(nextCursor=next_cursor, hasMore=next_cursor is not None, limit=applied_limit),
            )
            attempt.mark_success()
            return response
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            attempt.mark_rejection()
            raise
        finally:
            attempt.record()

    def get_concert_detail(self, concert_id: str) -> schemas.ConcertDetailResponse:
        """공연 상세 화면에 필요한 정보만 반환한다."""
        attempt = concert_metrics.start_catalog_query(CatalogResource.CONCERT)
        try:
            concert = self._concert(concert_id)
            if not concert.showtimes:
                raise PublicConcertNotFoundError(concert_id)
            response = concert_detail_response(concert)
            attempt.mark_success()
            return response
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            attempt.mark_rejection()
            raise
        finally:
            attempt.record()

    def get_monthly_availability(self, concert_id: str, year_month: str | None = None) -> schemas.ConcertCalendarResponse:
        """월 단위 날짜별 bookable 여부만 반환한다."""
        attempt = concert_metrics.start_catalog_query(CatalogResource.PERFORMANCES)
        try:
            self._ensure_concert_exists(concert_id)
            month_start = parse_year_month(year_month)
            year = month_start.year
            month = month_start.month
            _, days_in_month = monthrange(year, month)
            next_month = datetime(year + int(month == 12), 1 if month == 12 else month + 1, 1, tzinfo=UTC)
            bookable_dates = {
                starts_at.date()
                for starts_at in self.showtimes.list_bookable_showtime_starts_between(concert_id, month_start, next_month)
            }
            response = schemas.ConcertCalendarResponse(
                concertId=concert_id,
                yearMonth=f"{year:04d}-{month:02d}",
                timezone=SERVICE_TIMEZONE,
                days=[
                    schemas.CalendarDayResponse(
                        date=datetime(year, month, day, tzinfo=UTC).date(),
                        bookable=datetime(year, month, day, tzinfo=UTC).date() in bookable_dates,
                    )
                    for day in range(1, days_in_month + 1)
                ],
            )
            attempt.mark_success()
            return response
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            attempt.mark_rejection()
            raise
        finally:
            attempt.record()


def encode_recommended_cursor(created_at: datetime) -> str:
    payload = json.dumps({"createdAt": created_at.isoformat()}, separators=(",", ":")).encode()
    return base64.urlsafe_b64encode(payload).decode().rstrip("=")


def decode_recommended_cursor(cursor: str | None) -> datetime | None:
    if cursor is None:
        return None
    try:
        padded = cursor + "=" * ((4 - len(cursor) % 4) % 4)
        payload = json.loads(base64.urlsafe_b64decode(padded.encode()).decode())
        created_at = datetime.fromisoformat(payload["createdAt"])
    except (KeyError, ValueError, json.JSONDecodeError) as exc:
        raise InvalidPublicRequestError("cursor must be a recommended concerts cursor.") from exc
    return created_at


def parse_year_month(year_month: str | None) -> datetime:
    if year_month is None:
        now = now_utc()
        return datetime(now.year, now.month, 1, tzinfo=UTC)
    if re.fullmatch(r"\d{4}-\d{2}", year_month) is None:
        raise InvalidPublicRequestError("yearMonth must match YYYY-MM.")
    try:
        parsed = datetime.strptime(year_month, "%Y-%m")
    except ValueError as exc:
        raise InvalidPublicRequestError("yearMonth must match YYYY-MM.") from exc
    return parsed.replace(tzinfo=UTC)
