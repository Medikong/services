from fastapi import APIRouter, Depends, Query
from observability import trace_recorder
from sqlalchemy.orm import Session
from starlette.concurrency import run_in_threadpool

from app.auth import UserContext, get_user_context
from app.database import get_db
from app.kafka import KafkaProducer, get_kafka_producer
from app.schemas import TicketIssueRequest, TicketListResponse, TicketResponse
from app.services import ticket_service


router = APIRouter(prefix="/tickets", tags=["tickets"])


# 내부 또는 이벤트 기반 티켓 발행
@router.post("/issue", response_model=TicketResponse)
async def issue_ticket(
    request: TicketIssueRequest,
    db: Session = Depends(get_db),
    kafka_producer: KafkaProducer = Depends(get_kafka_producer),
) -> TicketResponse:
    return await ticket_service.issue_ticket(db, request, kafka_producer)


# 내 티켓 목록 조회
@router.get("/me", response_model=TicketListResponse)
def list_my_tickets(
    limit: int = Query(20, ge=1, le=100),
    cursor: str | None = Query(None, min_length=1),
    db: Session = Depends(get_db),
    user: UserContext = Depends(get_user_context),
) -> TicketListResponse:
    recorder = trace_recorder()
    with recorder.span(
        "ticket.list.route",
        {
            "ticket.list.limit": limit,
            "ticket.list.cursor_present": cursor is not None,
        },
    ):
        recorder.event("ticket.list.route.service_call.start")
        response = ticket_service.list_my_tickets(db, user, limit=limit, cursor=cursor, trace=recorder)
        recorder.event(
            "ticket.list.route.service_call.end",
            {
                "ticket.list.item_count": len(response.items),
                "ticket.list.has_next_cursor": response.nextCursor is not None,
            },
        )
        return response


@router.get("/me/async-experiment", response_model=TicketListResponse, include_in_schema=False)
async def list_my_tickets_async_experiment(
    limit: int = Query(20, ge=1, le=100),
    cursor: str | None = Query(None, min_length=1),
    db: Session = Depends(get_db),
    user: UserContext = Depends(get_user_context),
) -> TicketListResponse:
    recorder = trace_recorder()
    with recorder.span(
        "ticket.list.route.async_experiment",
        {
            "ticket.list.limit": limit,
            "ticket.list.cursor_present": cursor is not None,
        },
    ):
        recorder.event("ticket.list.route.threadpool_call.start")
        response = await run_in_threadpool(
            ticket_service.list_my_tickets,
            db,
            user,
            limit=limit,
            cursor=cursor,
            trace=recorder,
        )
        recorder.event(
            "ticket.list.route.threadpool_call.end",
            {
                "ticket.list.item_count": len(response.items),
                "ticket.list.has_next_cursor": response.nextCursor is not None,
            },
        )
        return response


# 티켓 상세 조회
@router.get("/{ticket_id}", response_model=TicketResponse)
def get_ticket(
    ticket_id: str,
    db: Session = Depends(get_db),
    user: UserContext = Depends(get_user_context),
) -> TicketResponse:
    return ticket_service.get_ticket(db, ticket_id, user)
