from datetime import datetime

from pydantic import BaseModel

from app.schemas.common import PageInfo
from app.schemas.concerts import ConcertDraftResponse
from app.schemas.policies import OpenRequestResponse, SalePolicyResponse


class ConcertReviewRequestResponse(BaseModel):
    id: str
    concertId: str
    providerId: str
    type: str
    status: str
    submittedAt: datetime
    concert: ConcertDraftResponse | None = None
    salePolicy: SalePolicyResponse | None = None
    openRequest: OpenRequestResponse | None = None


class ConcertReviewRequestListResponse(BaseModel):
    items: list[ConcertReviewRequestResponse]
    page: PageInfo
