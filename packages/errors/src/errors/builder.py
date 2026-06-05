from __future__ import annotations

from collections.abc import Iterable
from dataclasses import replace
from datetime import datetime, timezone
from typing import Any, Self

from errors.context import ExceptionContext, _attach_exception_context


class ExceptionContextBuilder:
    def __init__(self, context: ExceptionContext | None = None) -> None:
        self._context = context or ExceptionContext.empty()

    def build(self) -> ExceptionContext:
        return replace(self._context, attributes=dict(self._context.attributes))

    def in_domain(self, domain: str) -> Self:
        return self._with(domain=_required_text(domain, "domain"))

    def code(self, value: str) -> Self:
        return self._with(code=_required_text(value, "code"))

    def message(self, value: str) -> Self:
        return self._with(message=_required_text(value, "message"))

    def public(self, message: str) -> Self:
        return self._with(public_message=_required_text(message, "public_message"))

    def hint(self, value: str) -> Self:
        return self._with(hint=_required_text(value, "hint"))

    def owner(self, value: str) -> Self:
        return self._with(owner=_required_text(value, "owner"))

    def user(self, value: str) -> Self:
        return self._with(user=_required_text(value, "user"))

    def tenant(self, value: str) -> Self:
        return self._with(tenant=_required_text(value, "tenant"))

    def occurred_at(self, value: datetime) -> Self:
        return self._with(occurred_at=value)

    def duration_ms(self, value: float) -> Self:
        if value < 0:
            raise ValueError("duration_ms must be zero or greater")
        return self._with(duration_ms=value)

    def caused_by(self, exc: BaseException) -> Self:
        return self._with(cause=exc)

    def tag(self, value: str) -> Self:
        return self.tags(value)

    def tags(self, *values: str | Iterable[str]) -> Self:
        flattened = _flatten_tags(values)
        merged = _merge_tag_values(self._context.tags, flattened)
        return self._with(tags=merged)

    def with_attr(self, key: str, value: Any) -> Self:
        normalized_key = _required_text(key, "attribute key")
        attributes = dict(self._context.attributes)
        attributes[normalized_key] = value
        return self._with(attributes=attributes)

    def attach(self, exc: BaseException) -> BaseException:
        context = self.build()
        if context.occurred_at is None:
            context = replace(context, occurred_at=datetime.now(timezone.utc))
        if context.cause is None:
            context = replace(context, cause=exc.__cause__ or exc.__context__)
        _attach_exception_context(exc, context)
        return exc

    def _with(self, **changes: Any) -> Self:
        return type(self)(replace(self._context, **changes))


def errctx() -> ExceptionContextBuilder:
    return ExceptionContextBuilder()


def in_domain(domain: str) -> ExceptionContextBuilder:
    return errctx().in_domain(domain)


def code(value: str) -> ExceptionContextBuilder:
    return errctx().code(value)


def tag(value: str) -> ExceptionContextBuilder:
    return errctx().tag(value)


def tags(*values: str | Iterable[str]) -> ExceptionContextBuilder:
    return errctx().tags(*values)


def with_attr(key: str, value: Any) -> ExceptionContextBuilder:
    return errctx().with_attr(key, value)


def public(message: str) -> ExceptionContextBuilder:
    return errctx().public(message)


def hint(value: str) -> ExceptionContextBuilder:
    return errctx().hint(value)


def owner(value: str) -> ExceptionContextBuilder:
    return errctx().owner(value)


def _required_text(value: object, field_name: str) -> str:
    if not isinstance(value, str):
        raise TypeError(f"{field_name} must be a string")
    normalized = value.strip()
    if normalized == "":
        raise ValueError(f"{field_name} must not be empty")
    return normalized


def _flatten_tags(values: tuple[str | Iterable[str], ...]) -> tuple[str, ...]:
    flattened: list[str] = []
    for value in values:
        if isinstance(value, str):
            flattened.append(_required_text(value, "tag"))
            continue
        flattened.extend(_required_text(tag, "tag") for tag in value)
    return tuple(flattened)


def _merge_tag_values(existing: tuple[str, ...], incoming: tuple[str, ...]) -> tuple[str, ...]:
    merged: list[str] = []
    seen: set[str] = set()
    for tag in existing + incoming:
        if tag in seen:
            continue
        seen.add(tag)
        merged.append(tag)
    return tuple(merged)
