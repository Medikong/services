from datetime import UTC, datetime
from enum import StrEnum
from typing import Final

from fastapi import FastAPI, HTTPException, Query, status
from fastapi.responses import ORJSONResponse, PlainTextResponse
from pydantic import BaseModel, ConfigDict, Field


SERVICE_NAME: Final = "catalog-service"
SERVICE_VERSION: Final = "0.1.0"
SERVICE_ENVIRONMENT: Final = "local"


class DropStatus(StrEnum):
    UPCOMING = "UPCOMING"
    OPEN = "OPEN"
    SOLD_OUT = "SOLD_OUT"
    CLOSED = "CLOSED"


class ProductSummary(BaseModel):
    model_config = ConfigDict(frozen=True)

    id: str
    name: str
    price: int = Field(ge=0)
    remainingQuantity: int = Field(ge=0)


class DropSummary(BaseModel):
    model_config = ConfigDict(frozen=True)

    id: str
    title: str
    status: DropStatus
    opensAt: datetime
    closesAt: datetime | None = None
    products: tuple[ProductSummary, ...]


class DropDetail(DropSummary):
    description: str


class PageInfo(BaseModel):
    model_config = ConfigDict(frozen=True)

    nextCursor: str | None = None
    hasNext: bool


class DropListResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    data: tuple[DropSummary, ...]
    pageInfo: PageInfo


class DropDetailResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    data: DropDetail


class HealthResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    status: str
    service: str
    timestamp: datetime


class ReadinessResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    status: str
    service: str
    checks: dict[str, str]
    timestamp: datetime


DROP_CATALOG: Final = (
    DropDetail(
        id="drop-001",
        title="DropMong July Limited Drop",
        status=DropStatus.OPEN,
        opensAt=datetime(2026, 7, 3, 10, 0, tzinfo=UTC),
        closesAt=datetime(2026, 7, 10, 10, 0, tzinfo=UTC),
        description="한정 수량으로 판매되는 DropMong 첫 번째 공개 드롭입니다.",
        products=(
            ProductSummary(
                id="product-001",
                name="DropMong Starter Kit",
                price=50000,
                remainingQuantity=42,
            ),
        ),
    ),
    DropDetail(
        id="drop-sold-out-001",
        title="DropMong Sold Out Scenario Drop",
        status=DropStatus.OPEN,
        opensAt=datetime(2026, 7, 3, 10, 0, tzinfo=UTC),
        closesAt=datetime(2026, 7, 10, 10, 0, tzinfo=UTC),
        description="품절과 동시성 시나리오 검증을 위한 독립 드롭입니다.",
        products=(
            ProductSummary(
                id="product-sold-out-001",
                name="DropMong Concurrency Kit",
                price=50000,
                remainingQuantity=42,
            ),
        ),
    ),
)


def create_app() -> FastAPI:
    app = FastAPI(
        title="DropMong Catalog Service API",
        version=SERVICE_VERSION,
        default_response_class=ORJSONResponse,
    )

    @app.get("/healthz", response_model=HealthResponse)
    def healthz() -> HealthResponse:
        return HealthResponse(status="ok", service=SERVICE_NAME, timestamp=_utc_now())

    @app.get("/readyz", response_model=ReadinessResponse)
    def readyz() -> ReadinessResponse:
        return ReadinessResponse(
            status="ready",
            service=SERVICE_NAME,
            checks={"catalog": "ok"},
            timestamp=_utc_now(),
        )

    @app.get("/metrics", response_class=PlainTextResponse)
    def metrics() -> str:
        return (
            "# HELP service_ready Service readiness state. Ready is 1, not ready is 0.\n"
            "# TYPE service_ready gauge\n"
            'service_ready{service_name="catalog-service",service_version="0.1.0",service_environment="local"} 1\n'
        )

    @app.get("/drops", response_model=DropListResponse)
    def list_drops(
        limit: int = Query(default=20, ge=1, le=100),
        cursor: str | None = Query(default=None),
    ) -> DropListResponse:
        start = _start_index_after(cursor)
        selected = DROP_CATALOG[start : start + limit]
        has_next = start + limit < len(DROP_CATALOG)
        next_cursor = selected[-1].id if has_next and selected else None
        return DropListResponse(
            data=tuple(_drop_summary(drop) for drop in selected),
            pageInfo=PageInfo(nextCursor=next_cursor, hasNext=has_next),
        )

    @app.get("/drops/{drop_id}", response_model=DropDetailResponse)
    def get_drop(drop_id: str) -> DropDetailResponse:
        for drop in DROP_CATALOG:
            if drop.id == drop_id:
                return DropDetailResponse(data=drop)
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="drop not found")

    return app


def _drop_summary(drop: DropDetail) -> DropSummary:
    return DropSummary(
        id=drop.id,
        title=drop.title,
        status=drop.status,
        opensAt=drop.opensAt,
        closesAt=drop.closesAt,
        products=drop.products,
    )


def _start_index_after(cursor: str | None) -> int:
    if cursor is None:
        return 0
    for index, drop in enumerate(DROP_CATALOG):
        if drop.id == cursor:
            return index + 1
    return len(DROP_CATALOG)


def _utc_now() -> datetime:
    return datetime.now(UTC)


app = create_app()
