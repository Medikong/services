from fastapi import APIRouter

from app.routers import admin_policies, admin_reviews, provider_concerts, provider_policies, provider_seats, provider_venues, public


router = APIRouter()
router.include_router(public.router)
router.include_router(provider_venues.router)
router.include_router(provider_concerts.router)
router.include_router(provider_seats.router)
router.include_router(provider_policies.router)
router.include_router(admin_reviews.router)
router.include_router(admin_policies.router)

__all__ = ["router"]
