from datetime import datetime

from pydantic import BaseModel, Field


class SalePolicyUpdateRequest(BaseModel):
    presaleEnabled: bool = False
    fanclubVerificationRequired: bool = False
    maxTicketsPerUser: int = Field(ge=1)
    refundPolicy: str


class SalePolicyResponse(BaseModel):
    concertId: str
    presaleEnabled: bool = False
    fanclubVerificationRequired: bool = False
    maxTicketsPerUser: int
    refundPolicy: str
    status: str


class OpenRequestCreateRequest(BaseModel):
    requestedOpenAt: datetime
    message: str | None = None


class OpenRequestResponse(BaseModel):
    id: str
    concertId: str
    requestedOpenAt: datetime
    status: str
    message: str | None = None


class ReviewStatusResponse(BaseModel):
    concertId: str
    concertStatus: str
    salePolicyStatus: str
    openRequestStatus: str
    lastReviewedAt: datetime | None = None
    reason: str | None = None


class ApprovalCommand(BaseModel):
    comment: str | None = None


class RejectCommand(BaseModel):
    reason: str


class OpenScheduleUpdateRequest(BaseModel):
    opensAt: datetime
    comment: str | None = None


class OpenScheduleResponse(BaseModel):
    concertId: str
    opensAt: datetime
    status: str


class CanceledSeatReopenPolicyRequest(BaseModel):
    enabled: bool
    reopenDelaySeconds: int | None = Field(default=None, ge=0)
    batchSize: int | None = Field(default=None, ge=1)
    comment: str | None = None


class CanceledSeatReopenPolicyResponse(BaseModel):
    concertId: str
    enabled: bool
    reopenDelaySeconds: int | None = None
    batchSize: int | None = None
