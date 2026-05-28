from fastapi import APIRouter, Depends

from app.auth import UserContext, get_user_context
from app.database import get_db
from app.services import notification_service


router = APIRouter(prefix="/notifications", tags=["notifications"])


# 로그인한 사용자 본인의 알림 목록을 조회한다.
@router.get("")
async def list_notifications(
    user: UserContext = Depends(get_user_context),
) -> list[dict]:
    db = get_db()
    return await notification_service.list_notifications(db, user)


# 로그인한 사용자 본인의 알림 단건을 조회한다.
@router.get("/{notification_id}")
async def get_notification(
    notification_id: str,
    user: UserContext = Depends(get_user_context),
) -> dict:
    db = get_db()
    return await notification_service.get_notification(db, notification_id, user)
