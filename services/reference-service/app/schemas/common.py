from pydantic import BaseModel


class PageInfo(BaseModel):
    nextCursor: str | None = None
    hasNext: bool = False


class CursorPageInfo(BaseModel):
    nextCursor: str | None = None
    hasMore: bool = False
    limit: int
