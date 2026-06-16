from datetime import datetime

from pydantic import BaseModel, ConfigDict, Field, field_validator


class LoginRequest(BaseModel):
    email: str
    password: str


class SignupRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    email: str = Field(max_length=180)
    password: str = Field(min_length=8, max_length=128)
    displayName: str = Field(min_length=1, max_length=100)

    @field_validator("email")
    @classmethod
    def normalize_email(cls, value: str) -> str:
        normalized = value.strip().lower()
        if "@" not in normalized or normalized.startswith("@") or normalized.endswith("@"):
            raise ValueError("email must be a valid email address")
        return normalized

    @field_validator("displayName")
    @classmethod
    def normalize_display_name(cls, value: str) -> str:
        normalized = value.strip()
        if not normalized:
            raise ValueError("displayName must not be blank")
        return normalized


class UserResponse(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: str
    email: str
    displayName: str
    role: str
    isActive: bool
    createdAt: datetime

    @field_validator("id", mode="before")
    @classmethod
    def stringify_id(cls, value: object) -> str:
        return str(value)


class TokenResponse(BaseModel):
    accessToken: str
    refreshToken: str
    tokenType: str = "Bearer"
    expiresIn: int
    refreshExpiresIn: int
    user: UserResponse


class RefreshTokenRequest(BaseModel):
    refreshToken: str


class LogoutRequest(BaseModel):
    refreshToken: str | None = None


class DemoAccountResponse(BaseModel):
    email: str
    password: str
    displayName: str
    role: str


class HealthResponse(BaseModel):
    status: str
    service: str
    timestamp: datetime


class ReadinessResponse(BaseModel):
    status: str
    service: str
    checks: dict[str, str]
    timestamp: datetime


class AuditLogResponse(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: int
    eventType: str
    outcome: str
    userId: int | None = None
    userEmail: str | None = None
    role: str | None = None
    requestId: str | None = None
    method: str | None = None
    path: str | None = None
    clientIp: str | None = None
    userAgent: str | None = None
    details: str | None = None
    createdAt: datetime
