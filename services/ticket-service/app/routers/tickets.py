from fastapi import APIRouter, Depends
from sqlalchemy.orm import Session

from app.auth import UserContext, get_user_context
from app.database import get_db
from app.schemas import TicketIssueRequest, TicketResponse
from app.services import ticket_service


router = APIRouter(prefix="/tickets", tags=["tickets"])


# 내부 또는 이벤트 기반 티켓 발행
@router.post("/issue", response_model=TicketResponse)
async def issue_ticket(
    request: TicketIssueRequest,
    db: Session = Depends(get_db),
) -> TicketResponse:
    return await ticket_service.issue_ticket(db, request)


# 티켓 상세 조회
@router.get("/{ticket_id}", response_model=TicketResponse)
def get_ticket(
    ticket_id: int,
    db: Session = Depends(get_db),
    user: UserContext = Depends(get_user_context),
) -> TicketResponse:
    return ticket_service.get_ticket(db, ticket_id, user)


# 내 티켓 목록 조회
@router.get("/me", response_model=list[TicketResponse])
def list_my_tickets(
    db: Session = Depends(get_db),
    user: UserContext = Depends(get_user_context),
) -> list[TicketResponse]:
    return ticket_service.list_my_tickets(db, user)
