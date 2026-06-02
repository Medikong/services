from datetime import UTC, datetime
from uuid import uuid4

from fastapi import Depends, FastAPI, Header, HTTPException, Request, status
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from sqlalchemy import func
from sqlalchemy.orm import Session
from server.operational import register_operational_handlers, sqlalchemy_readiness_check

from app import kafka, models
from app.auth import UserContext, require_role, require_user_context
from app.config import settings
from app.database import engine, get_db
from app.models import Payment, PaymentEvent
from app.observability import setup_request_logging
from app.schemas import CreatePaymentRequest, PaymentResponse, SettlementBasisResponse


models.Base.metadata.create_all(bind=engine)

app = FastAPI(title=settings.service_name)
setup_request_logging(app, settings.observability_config())
register_operational_handlers(
    app,
    service_name=settings.service_name,
    readiness_checks={"database": sqlalchemy_readiness_check(engine)},
    include_timestamp=True,
)


@app.exception_handler(HTTPException)
async def http_exception_handler(request: Request, exc: HTTPException) -> JSONResponse:
    return _error_response(request, exc.status_code, _error_code_for_status(exc.status_code), str(exc.detail))


@app.exception_handler(RequestValidationError)
async def validation_exception_handler(request: Request, exc: RequestValidationError) -> JSONResponse:
    return _error_response(
        request,
        status.HTTP_422_UNPROCESSABLE_ENTITY,
        "request.validation_failed",
        "Request validation failed.",
        {"errors": exc.errors()},
    )


@app.get("/health")
def health() -> dict[str, str]:
    return {"status": "ok", "service": settings.service_name}


@app.post("/payments", response_model=PaymentResponse, status_code=status.HTTP_201_CREATED)
async def create_payment(
    request_body: CreatePaymentRequest,
    request: Request,
    idempotency_key: str | None = Header(default=None, alias="Idempotency-Key"),
    user: UserContext = Depends(require_user_context),
    db: Session = Depends(get_db),
) -> PaymentResponse:
    require_role(user, {"CUSTOMER"})

    if idempotency_key:
        existing = (
            db.query(Payment)
            .filter(Payment.user_id == user.user_id, Payment.idempotency_key == idempotency_key)
            .one_or_none()
        )
        if existing is not None:
            return PaymentResponse.model_validate(existing)

    payment = Payment(
        id=f"pay-{uuid4()}",
        reservation_id=request_body.reservationId,
        concert_id=request_body.concertId,
        user_id=user.user_id,
        amount=request_body.amount,
        method=request_body.method,
        status=_status_from_simulation(request_body.simulation),
        idempotency_key=idempotency_key,
        approved_at=datetime.now(UTC) if request_body.simulation == "approve" else None,
    )
    db.add(payment)
    event_name = _payment_event_name(payment.status)
    event_payload = None
    if event_name is not None:
        event_id = f"evt-{uuid4()}"
        event_payload = _payment_event_payload(
            event_id=event_id,
            event_name=event_name,
            payment=payment,
            request_body=request_body,
            user=user,
            request=request,
        )
        db.add(
            PaymentEvent(
                id=event_id,
                event_type=event_name,
                payment_id=payment.id,
                payload=event_payload,
            )
        )
    db.commit()
    db.refresh(payment)
    request.state.payment_event = event_name
    if event_name is not None and event_payload is not None:
        await kafka.publish_event(_payment_event_topic(event_name), event_payload)
    return PaymentResponse.model_validate(payment)


@app.get("/payments/{paymentId}", response_model=PaymentResponse)
def get_payment(
    paymentId: str,
    user: UserContext = Depends(require_user_context),
    db: Session = Depends(get_db),
) -> PaymentResponse:
    payment = db.get(Payment, paymentId)
    if payment is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="Payment not found")
    if user.role != "ADMIN" and payment.user_id != user.user_id:
        raise HTTPException(status_code=status.HTTP_403_FORBIDDEN, detail="Forbidden")
    return PaymentResponse.model_validate(payment)


@app.get("/provider/concerts/{concertId}/settlement-basis", response_model=SettlementBasisResponse)
def provider_get_settlement_basis(
    concertId: str,
    user: UserContext = Depends(require_user_context),
    db: Session = Depends(get_db),
) -> SettlementBasisResponse:
    require_role(user, {"PROVIDER", "ADMIN"})
    return _settlement_for_concert(concertId, db)


@app.get("/admin/concerts/{concertId}/settlement-basis", response_model=SettlementBasisResponse)
def admin_get_settlement_basis(
    concertId: str,
    user: UserContext = Depends(require_user_context),
    db: Session = Depends(get_db),
) -> SettlementBasisResponse:
    require_role(user, {"ADMIN"})
    return _settlement_for_concert(concertId, db)


def _status_from_simulation(simulation: str) -> str:
    if simulation == "approve":
        return "approved"
    if simulation == "fail":
        return "failed"
    if simulation == "delay":
        return "delayed"
    raise HTTPException(status_code=status.HTTP_422_UNPROCESSABLE_ENTITY, detail="Invalid payment simulation")


def _payment_event_name(payment_status: str) -> str | None:
    if payment_status == "approved":
        return "payment-approved"
    if payment_status == "failed":
        return "payment-failed"
    return None


def _payment_event_topic(event_name: str) -> str:
    if event_name == "payment-approved":
        return settings.payment_approved_topic
    if event_name == "payment-failed":
        return settings.payment_failed_topic
    return event_name


def _payment_event_payload(
    *,
    event_id: str,
    event_name: str,
    payment: Payment,
    request_body: CreatePaymentRequest,
    user: UserContext,
    request: Request,
) -> dict:
    return {
        "eventId": event_id,
        "eventType": event_name,
        "userId": _event_user_id(user.user_id),
        "sourceId": payment.id,
        "paymentId": payment.id,
        "reservationId": payment.reservation_id,
        "concertId": payment.concert_id,
        "seatId": request_body.seatId or "unknown",
        "amount": payment.amount,
        "status": payment.status,
        "occurredAt": datetime.now(UTC).isoformat(),
        "producer": settings.service_name,
        "correlationId": getattr(request.state, "request_id", None) or request.headers.get("X-Request-Id"),
    }


def _event_user_id(value: str) -> int | str:
    return int(value) if value.isdigit() else value


def _settlement_for_concert(concert_id: str, db: Session) -> SettlementBasisResponse:
    gross_amount = (
        db.query(func.coalesce(func.sum(Payment.amount), 0))
        .filter(Payment.concert_id == concert_id, Payment.status == "approved")
        .scalar()
    )
    ticket_count = (
        db.query(func.count(Payment.id))
        .filter(Payment.concert_id == concert_id, Payment.status == "approved")
        .scalar()
    )
    gross = int(gross_amount or 0)
    count = int(ticket_count or 0)
    refund = 0
    platform_fee = int(gross * 0.1)
    net = gross - refund
    return SettlementBasisResponse(
        concertId=concert_id,
        grossAmount=gross,
        refundAmount=refund,
        netAmount=net,
        ticketCount=count,
        platformFeeAmount=platform_fee,
        providerSettlementAmount=net - platform_fee,
        calculatedAt=datetime.now(UTC),
    )


def _error_response(
    request: Request,
    status_code: int,
    code: str,
    message: str,
    details: dict | None = None,
) -> JSONResponse:
    request_id = getattr(request.state, "request_id", None) or request.headers.get("X-Request-Id") or ""
    body = {
        "error": {
            "code": code,
            "message": message,
        },
        "requestId": request_id,
        "occurredAt": datetime.now(UTC).isoformat(),
    }
    if details:
        body["error"]["details"] = details
    return JSONResponse(status_code=status_code, content=body)


def _error_code_for_status(status_code: int) -> str:
    if status_code == status.HTTP_401_UNAUTHORIZED:
        return "auth.invalid_token"
    if status_code == status.HTTP_403_FORBIDDEN:
        return "auth.forbidden"
    if status_code == status.HTTP_404_NOT_FOUND:
        return "payment.not_found"
    if status_code == status.HTTP_503_SERVICE_UNAVAILABLE:
        return "service.unavailable"
    return "request.failed"
