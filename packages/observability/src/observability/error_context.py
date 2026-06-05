from collections.abc import Mapping
from typing import Protocol, runtime_checkable


SafeErrorContext = Mapping[str, str | int | float | bool | None]


@runtime_checkable
class SupportsErrorContext(Protocol):
    # 나중에 packages/errors의 context를 읽기 위한 자리다. errors 쪽이 FastAPI나 OpenTelemetry를 알면 안 된다.
    def observability_context(self) -> SafeErrorContext:
        ...


def extract_error_context(exc: BaseException) -> SafeErrorContext:
    if isinstance(exc, SupportsErrorContext):
        return exc.observability_context()

    context = getattr(exc, "error_context", None)
    if isinstance(context, Mapping):
        return {str(key): value for key, value in context.items() if _is_safe_context_value(value)}

    return {}


def _is_safe_context_value(value: object) -> bool:
    return value is None or isinstance(value, str | int | float | bool)
