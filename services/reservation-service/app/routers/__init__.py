from fastapi import APIRouter

from app.routers import admin_policies, admin_sales, provider_sales, reservations


router = APIRouter()
router.include_router(reservations.router)
router.include_router(provider_sales.router)
router.include_router(admin_sales.router)
router.include_router(admin_policies.router)

__all__ = ["router"]
