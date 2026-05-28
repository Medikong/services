from pydantic import BaseModel


class PageInfo(BaseModel):
    nextCursor: str | None = None
    hasNext: bool = False
