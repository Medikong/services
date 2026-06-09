from datetime import UTC, datetime

from fastapi import Body, Depends, FastAPI, Header, HTTPException, Request, status
from observability import register_error_handlers
from sqlalchemy.orm import Session
from server.operational import register_operational_handlers, sqlalchemy_readiness_check

from app import models
from app.audit import record_audit
from app.config import settings
from app.database import SessionLocal, engine, get_db
from app.models import AuditLog, RefreshToken, RevokedToken, User
from app.observability import configure_app_observability
from app.schemas import (
    AuditLogResponse,
    DemoAccountResponse,
    LoginRequest,
    LogoutRequest,
    RefreshTokenRequest,
    TokenResponse,
    UserResponse,
)
from app.security import create_access_token, create_refresh_token, decode_access_token, hash_refresh_token, verify_password
from app.seed import DEMO_USERS, seed_demo_users


models.Base.metadata.create_all(bind=engine)
with SessionLocal() as seed_db:
    seed_demo_users(seed_db)

observability_config = settings.observability_config()
app = FastAPI(title=settings.service_name)
configure_app_observability(app, observability_config)
register_error_handlers(
    app,
    service_name=settings.service_name,
    domain="auth",
    http_error_code_for_status=lambda status_code: _error_code_for_status(status_code),
)
register_operational_handlers(
    app,
    service_name=settings.service_name,
    service_version=observability_config.service_version,
    service_environment=observability_config.service_environment,
    readiness_checks={"database": sqlalchemy_readiness_check(engine)},
    include_timestamp=True,
)


@app.get("/health")
def health() -> dict[str, str]:
    return {"status": "ok", "service": settings.service_name}


@app.post("/auth/login", response_model=TokenResponse)
def login(request_body: LoginRequest, request: Request, db: Session = Depends(get_db)) -> TokenResponse:
    user = db.query(User).filter(User.email == request_body.email.lower()).one_or_none()
    if user is None or not verify_password(request_body.password, user.password_hash):
        record_audit(
            db,
            request,
            event_type="LOGIN_FAILED",
            outcome="DENIED",
            user_email=request_body.email.lower(),
            details="invalid credentials",
        )
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="Invalid email or password")
    if not user.is_active:
        record_audit(db, request, event_type="LOGIN_FAILED", outcome="DENIED", user=user, details="inactive account")
        raise HTTPException(status_code=status.HTTP_403_FORBIDDEN, detail="Inactive account")

    token = _issue_token_response(db, user)
    record_audit(db, request, event_type="LOGIN_SUCCEEDED", outcome="ALLOW", user=user)
    return token


@app.get("/auth/demo-accounts", response_model=list[DemoAccountResponse])
def demo_accounts() -> list[DemoAccountResponse]:
    if not settings.expose_demo_accounts:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="Demo accounts are disabled")
    return [
        DemoAccountResponse(
            email=str(account["email"]),
            password=str(account["password"]),
            displayName=str(account["display_name"]),
            role=str(account["role"]),
        )
        for account in DEMO_USERS
    ]


@app.get("/auth/me", response_model=UserResponse)
def me(
    request: Request,
    authorization: str | None = Header(default=None, alias="Authorization"),
    db: Session = Depends(get_db),
) -> UserResponse:
    payload = _require_valid_payload(authorization, db)
    user = _get_user_from_payload(payload, db)
    record_audit(db, request, event_type="ME_VIEWED", outcome="ALLOW", user=user)
    return UserResponse.model_validate(user)


@app.post("/auth/refresh", response_model=TokenResponse)
def refresh_token(
    request_body: RefreshTokenRequest,
    request: Request,
    db: Session = Depends(get_db),
) -> TokenResponse:
    stored_token = _get_active_refresh_token(request_body.refreshToken, db)
    user = db.get(User, stored_token.user_id)
    if user is None or not user.is_active:
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="User not found")

    stored_token.revoked_at = datetime.now(UTC)
    token_response = _issue_token_response(db, user)
    db.commit()
    record_audit(db, request, event_type="TOKEN_REFRESHED", outcome="ALLOW", user=user)
    return token_response


@app.post("/auth/logout")
def logout(
    request: Request,
    request_body: LogoutRequest | None = Body(default=None),
    authorization: str | None = Header(default=None, alias="Authorization"),
    db: Session = Depends(get_db),
) -> dict[str, str]:
    payload = _require_valid_payload(authorization, db)
    user = _get_user_from_payload(payload, db)
    token_id = str(payload["jti"])
    expires_at = datetime.fromtimestamp(int(payload["exp"]), UTC)
    if db.query(RevokedToken).filter(RevokedToken.token_id == token_id).one_or_none() is None:
        db.add(RevokedToken(token_id=token_id, user_id=user.id, expires_at=expires_at))
    if request_body and request_body.refreshToken:
        _revoke_refresh_token(request_body.refreshToken, db)
    db.commit()
    record_audit(db, request, event_type="LOGOUT", outcome="ALLOW", user=user)
    return {"status": "ok"}


@app.get("/auth/audit-logs", response_model=list[AuditLogResponse])
def audit_logs(
    request: Request,
    authorization: str | None = Header(default=None, alias="Authorization"),
    db: Session = Depends(get_db),
) -> list[AuditLogResponse]:
    payload = _require_valid_payload(authorization, db)
    user = _get_user_from_payload(payload, db)
    if user.role != "ADMIN":
        record_audit(db, request, event_type="AUDIT_LOG_VIEW_DENIED", outcome="DENIED", user=user)
        raise HTTPException(status_code=status.HTTP_403_FORBIDDEN, detail="ADMIN role required")
    record_audit(db, request, event_type="AUDIT_LOG_VIEWED", outcome="ALLOW", user=user)
    logs = db.query(AuditLog).order_by(AuditLog.id.desc()).limit(100).all()
    return [AuditLogResponse.model_validate(log) for log in logs]


def _require_valid_payload(authorization: str | None, db: Session) -> dict:
    if not authorization:
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="Missing Authorization header")
    scheme, _, token = authorization.partition(" ")
    if scheme.lower() != "bearer" or not token:
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="Invalid Authorization header")
    payload = decode_access_token(token)
    token_id = str(payload.get("jti", ""))
    if not token_id:
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="Missing token id")
    if db.query(RevokedToken).filter(RevokedToken.token_id == token_id).one_or_none() is not None:
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="Token revoked")
    return payload


def _get_user_from_payload(payload: dict, db: Session) -> User:
    user = db.get(User, int(payload["sub"]))
    if user is None or not user.is_active:
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="User not found")
    return user


def _issue_token_response(db: Session, user: User) -> TokenResponse:
    access_token, _token_id, _expires_at = create_access_token(
        user_id=user.id,
        email=user.email,
        role=user.role,
    )
    refresh_token, token_hash, refresh_expires_at = create_refresh_token()
    db.add(RefreshToken(token_hash=token_hash, user_id=user.id, expires_at=refresh_expires_at))
    db.flush()
    return TokenResponse(
        accessToken=access_token,
        refreshToken=refresh_token,
        expiresIn=settings.token_ttl_seconds,
        refreshExpiresIn=settings.refresh_token_ttl_seconds,
        user=UserResponse.model_validate(user),
    )


def _get_active_refresh_token(refresh_token: str, db: Session) -> RefreshToken:
    stored_token = db.query(RefreshToken).filter(RefreshToken.token_hash == hash_refresh_token(refresh_token)).one_or_none()
    if stored_token is None or stored_token.revoked_at is not None or _is_expired(stored_token.expires_at):
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="Invalid refresh token")
    return stored_token


def _revoke_refresh_token(refresh_token: str, db: Session) -> None:
    stored_token = db.query(RefreshToken).filter(RefreshToken.token_hash == hash_refresh_token(refresh_token)).one_or_none()
    if stored_token is not None and stored_token.revoked_at is None:
        stored_token.revoked_at = datetime.now(UTC)


def _is_expired(value: datetime) -> bool:
    if value.tzinfo is None:
        value = value.replace(tzinfo=UTC)
    return value <= datetime.now(UTC)


def _error_code_for_status(status_code: int) -> str:
    if status_code == status.HTTP_401_UNAUTHORIZED:
        return "auth.invalid_token"
    if status_code == status.HTTP_403_FORBIDDEN:
        return "auth.forbidden"
    if status_code == status.HTTP_503_SERVICE_UNAVAILABLE:
        return "service.unavailable"
    return "request.failed"
