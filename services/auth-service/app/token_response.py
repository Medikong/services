from sqlalchemy.orm import Session

from app.config import settings
from app.metrics.events import AuthTokenIssuedRecorded
from app.metrics.labels import AuthTokenType
from app.metrics.recorder import AuthTelemetryRecorder
from app.models import RefreshToken, User
from app.schemas import TokenResponse, UserResponse
from app.security import create_access_token, create_refresh_token


def issue_token_response(db: Session, user: User, auth_metrics: AuthTelemetryRecorder) -> TokenResponse:
    access_token, _token_id, _expires_at = create_access_token(
        user_id=user.id,
        email=user.email,
        role=user.role,
    )
    refresh_token, token_hash, refresh_expires_at = create_refresh_token()
    db.add(RefreshToken(token_hash=token_hash, user_id=user.id, expires_at=refresh_expires_at))
    db.flush()
    auth_metrics.record(AuthTokenIssuedRecorded(token_type=AuthTokenType.ACCESS))
    auth_metrics.record(AuthTokenIssuedRecorded(token_type=AuthTokenType.REFRESH))
    return TokenResponse(
        accessToken=access_token,
        refreshToken=refresh_token,
        expiresIn=settings.token_ttl_seconds,
        refreshExpiresIn=settings.refresh_token_ttl_seconds,
        user=UserResponse.model_validate(user),
    )
