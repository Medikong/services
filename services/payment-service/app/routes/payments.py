from fastapi import APIRouter, Depends, Header, Request, status
from sqlalchemy.orm import Session

from app.auth import UserContext, require_role, require_user_context
from app.database import get_db
from app.metrics.recorder import PaymentTelemetryRecorder
from app.schemas import CreatePaymentRequest, PaymentResponse, SettlementBasisResponse
from app.services.payments import PaymentRequestContext, PaymentService


router = APIRouter()


def get_payment_service(db: Session = Depends(get_db)) -> PaymentService:
    """요청 처리에 사용할 결제 use case 객체를 생성한다."""
    # 요청마다 DB 세션과 telemetry recorder를 묶어 use case를 만든다.
    telemetry = PaymentTelemetryRecorder()
    return PaymentService(
        db=db,
        telemetry=telemetry,
    )


@router.post("/payments", response_model=PaymentResponse, status_code=status.HTTP_201_CREATED)
async def create_payment(
    request_body: CreatePaymentRequest,
    request: Request,
    idempotency_key: str | None = Header(default=None, alias="Idempotency-Key"),
    user: UserContext = Depends(require_user_context),
    payment_service: PaymentService = Depends(get_payment_service),
) -> PaymentResponse:
    """결제 생성 HTTP 요청을 use case로 위임하고 응답 모델로 변환한다."""
    require_role(user, {"CUSTOMER"})

    # HTTP 요청 정보는 use case가 필요한 낮은 수준의 context로만 넘긴다.
    context = PaymentRequestContext(
        idempotency_key=idempotency_key,
        correlation_id=(
            getattr(request.state, "request_id", None)
            or request.headers.get("X-Request-Id")
        ),
    )

    result = await payment_service.create_payment(
        request_body=request_body,
        user=user,
        context=context,
    )

    # 이후 middleware/logging에서 참조할 수 있도록 이벤트 종류만 state에 남긴다.
    request.state.payment_event = result.event_type.value if result.event_type is not None else None
    return PaymentResponse.model_validate(result.payment)


@router.get("/payments/{paymentId}", response_model=PaymentResponse)
def get_payment(
    paymentId: str,
    user: UserContext = Depends(require_user_context),
    payment_service: PaymentService = Depends(get_payment_service),
) -> PaymentResponse:
    """결제 상세 조회 요청을 처리한다."""
    payment = payment_service.get_payment(payment_id=paymentId, user=user)
    return PaymentResponse.model_validate(payment)


@router.get("/provider/concerts/{concertId}/settlement-basis", response_model=SettlementBasisResponse)
def provider_get_settlement_basis(
    concertId: str,
    user: UserContext = Depends(require_user_context),
    payment_service: PaymentService = Depends(get_payment_service),
) -> SettlementBasisResponse:
    """공급자 권한으로 공연 정산 기준을 조회한다."""
    require_role(user, {"PROVIDER", "ADMIN"})
    # 공급자/관리자 정산 조회는 같은 서비스 메서드를 공유한다.
    return payment_service.settlement_for_concert(concertId)


@router.get("/admin/concerts/{concertId}/settlement-basis", response_model=SettlementBasisResponse)
def admin_get_settlement_basis(
    concertId: str,
    user: UserContext = Depends(require_user_context),
    payment_service: PaymentService = Depends(get_payment_service),
) -> SettlementBasisResponse:
    """관리자 권한으로 공연 정산 기준을 조회한다."""
    require_role(user, {"ADMIN"})
    return payment_service.settlement_for_concert(concertId)
