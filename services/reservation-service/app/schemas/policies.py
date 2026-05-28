from pydantic import BaseModel, Field


class QueuePolicyUpdateRequest(BaseModel):
    enabled: bool
    maxEntrantsPerMinute: int | None = Field(default=None, ge=1)
    waitingRoomUrl: str | None = None


class QueuePolicyResponse(BaseModel):
    concertId: str
    enabled: bool
    maxEntrantsPerMinute: int | None = None
    waitingRoomUrl: str | None = None


class TrafficPolicyUpdateRequest(BaseModel):
    macroProtectionEnabled: bool
    maxRequestsPerUserPerMinute: int | None = Field(default=None, ge=1)
    blockSuspiciousTraffic: bool = True


class TrafficPolicyResponse(BaseModel):
    concertId: str
    macroProtectionEnabled: bool
    maxRequestsPerUserPerMinute: int | None = None
    blockSuspiciousTraffic: bool = True
