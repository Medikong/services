"""Public API for exception context propagation."""

from errors.builder import (
    ExceptionContextBuilder,
    code,
    errctx,
    hint,
    in_domain,
    owner,
    public,
    tag,
    tags,
    with_attr,
)
from errors.context import ExceptionContext, get_exception_context
from errors.exceptions import ContextualError

__all__ = [
    "ContextualError",
    "ExceptionContext",
    "ExceptionContextBuilder",
    "code",
    "errctx",
    "get_exception_context",
    "hint",
    "in_domain",
    "owner",
    "public",
    "tag",
    "tags",
    "with_attr",
]
