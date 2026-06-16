from fastapi import APIRouter, Depends, HTTPException, Request, status
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from app.audit import record_audit
from app.database import get_db
from app.metrics.recorder import AuthTelemetryRecorder
from app.models import User
from app.schemas import SignupRequest, TokenResponse
from app.security import hash_password
from app.token_response import issue_token_response


router = APIRouter(prefix="/auth", tags=["Auth API"])
auth_metrics = AuthTelemetryRecorder()


@router.post("/signup", response_model=TokenResponse, status_code=status.HTTP_201_CREATED)
def signup(request_body: SignupRequest, request: Request, db: Session = Depends(get_db)) -> TokenResponse:
    email = request_body.email
    if db.query(User).filter(User.email == email).one_or_none() is not None:
        _record_duplicate_signup(db, request, email)
        raise HTTPException(status_code=status.HTTP_409_CONFLICT, detail="Email already registered")

    user = User(
        email=email,
        password_hash=hash_password(request_body.password),
        display_name=request_body.displayName,
        role="CUSTOMER",
        is_active=True,
    )
    db.add(user)
    try:
        db.flush()
    except IntegrityError as exc:
        db.rollback()
        _record_duplicate_signup(db, request, email)
        raise HTTPException(status_code=status.HTTP_409_CONFLICT, detail="Email already registered") from exc

    token = issue_token_response(db, user, auth_metrics)
    record_audit(db, request, event_type="SIGNUP_SUCCEEDED", outcome="ALLOW", user=user)
    return token


def _record_duplicate_signup(db: Session, request: Request, email: str) -> None:
    record_audit(
        db,
        request,
        event_type="SIGNUP_FAILED",
        outcome="DENIED",
        user_email=email,
        details="duplicate email",
    )
