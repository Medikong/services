from __future__ import annotations

from collections.abc import Iterator
from dataclasses import dataclass, field, replace
from datetime import datetime
from typing import Any, Final


_CONTEXT_ATTR: Final = "__medikong_exception_context__"


@dataclass(frozen=True)
class ExceptionContext:
    code: str | None = None
    domain: str | None = None
    message: str | None = None
    public_message: str | None = None
    tags: tuple[str, ...] = ()
    attributes: dict[str, Any] = field(default_factory=dict)
    hint: str | None = None
    owner: str | None = None
    user: str | None = None
    tenant: str | None = None
    occurred_at: datetime | None = None
    duration_ms: float | None = None
    cause: BaseException | None = None

    @classmethod
    def empty(cls) -> ExceptionContext:
        return cls()

    @property
    def is_empty(self) -> bool:
        return (
            self.code is None
            and self.domain is None
            and self.message is None
            and self.public_message is None
            and not self.tags
            and not self.attributes
            and self.hint is None
            and self.owner is None
            and self.user is None
            and self.tenant is None
            and self.occurred_at is None
            and self.duration_ms is None
            and self.cause is None
        )

    def merge(self, incoming: ExceptionContext) -> ExceptionContext:
        return ExceptionContext(
            code=self.code or incoming.code,
            domain=self.domain or incoming.domain,
            message=self.message or incoming.message,
            public_message=self.public_message or incoming.public_message,
            tags=_merge_tags(self.tags, incoming.tags),
            attributes={**incoming.attributes, **self.attributes},
            hint=self.hint or incoming.hint,
            owner=self.owner or incoming.owner,
            user=self.user or incoming.user,
            tenant=self.tenant or incoming.tenant,
            occurred_at=self.occurred_at or incoming.occurred_at,
            duration_ms=self.duration_ms if self.duration_ms is not None else incoming.duration_ms,
            cause=self.cause or incoming.cause,
        )


def get_exception_context(exc: BaseException) -> ExceptionContext:
    context = _get_attached_context(exc)
    if context is not None:
        return context

    for chained in _iter_exception_chain(exc):
        chained_context = _get_attached_context(chained)
        if chained_context is not None:
            if chained_context.cause is None:
                return replace(chained_context, cause=chained)
            return chained_context

    return ExceptionContext.empty()


def _attach_exception_context(exc: BaseException, context: ExceptionContext) -> None:
    existing = _get_attached_context(exc)
    merged = existing.merge(context) if existing is not None else context
    setattr(exc, _CONTEXT_ATTR, merged)


def _get_attached_context(exc: BaseException) -> ExceptionContext | None:
    context = getattr(exc, _CONTEXT_ATTR, None)
    if isinstance(context, ExceptionContext):
        return context
    return None


def _iter_exception_chain(exc: BaseException) -> Iterator[BaseException]:
    seen: set[int] = {id(exc)}
    pending = [exc.__cause__, exc.__context__]

    while pending:
        current = pending.pop(0)
        if current is None or id(current) in seen:
            continue
        seen.add(id(current))
        yield current
        pending.extend([current.__cause__, current.__context__])


def _merge_tags(existing: tuple[str, ...], incoming: tuple[str, ...]) -> tuple[str, ...]:
    merged: list[str] = []
    seen: set[str] = set()
    for tag in existing + incoming:
        if tag in seen:
            continue
        seen.add(tag)
        merged.append(tag)
    return tuple(merged)
