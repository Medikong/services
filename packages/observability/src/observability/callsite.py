from __future__ import annotations

from collections.abc import Sequence
from dataclasses import dataclass
import inspect
from threading import RLock
from typing import Any

from observability.config import DEFAULT_CALLSITE_MODULE_PREFIXES


TraceCallsiteAttributes = dict[str, str | int]

_MAX_CALLSITE_CACHE_SIZE = 1024
_callsite_cache: dict[str, "Callsite"] = {}
_callsite_lock = RLock()


@dataclass(frozen=True)
class Callsite:
    namespace: str
    function_name: str
    file_path: str
    line_number: int

    def location(self) -> str:
        return f"{self.namespace}.{self.function_name} {self.file_path}:{self.line_number}"

    def as_trace_attributes(self) -> TraceCallsiteAttributes:
        # Stable code semantic conventions:
        # https://opentelemetry.io/docs/specs/semconv/registry/attributes/code/
        return {
            "code.function.name": self.function_name,
            "code.location": self.location(),
        }


def get_callsite(key: str) -> Callsite | None:
    with _callsite_lock:
        return _callsite_cache.get(_require_callsite_key(key))


def put_callsite(key: str, callsite: Callsite) -> None:
    cache_key = _require_callsite_key(key)
    with _callsite_lock:
        if cache_key not in _callsite_cache and len(_callsite_cache) >= _MAX_CALLSITE_CACHE_SIZE:
            _callsite_cache.pop(next(iter(_callsite_cache)))
        _callsite_cache[cache_key] = callsite


def list_callsites() -> dict[str, Callsite]:
    with _callsite_lock:
        return dict(_callsite_cache)


def clear_callsite_cache() -> None:
    with _callsite_lock:
        _callsite_cache.clear()


def find_application_callsite(
    module_prefixes: Sequence[str] = DEFAULT_CALLSITE_MODULE_PREFIXES,
) -> Callsite | None:
    accepted_prefixes = _normalize_module_prefixes(module_prefixes)
    frame = inspect.currentframe()
    try:
        frame = frame.f_back if frame is not None else None
        while frame is not None:
            if _is_application_frame(frame, accepted_prefixes):
                return Callsite(
                    namespace=str(frame.f_globals.get("__name__", "")),
                    function_name=frame.f_code.co_name,
                    file_path=frame.f_code.co_filename,
                    line_number=frame.f_lineno,
                )
            frame = frame.f_back
        return None
    finally:
        del frame


def _require_callsite_key(key: str) -> str:
    if not key:
        raise ValueError("callsite key must not be empty")
    return key


def _is_application_frame(frame: Any, module_prefixes: tuple[str, ...]) -> bool:
    module = str(frame.f_globals.get("__name__", ""))
    return any(_module_matches_prefix(module, prefix) for prefix in module_prefixes)


def _module_matches_prefix(module: str, prefix: str) -> bool:
    return module == prefix or module.startswith(f"{prefix}.")


def _normalize_module_prefixes(module_prefixes: Sequence[str]) -> tuple[str, ...]:
    prefixes = tuple(prefix.strip() for prefix in module_prefixes if prefix.strip())
    return prefixes or DEFAULT_CALLSITE_MODULE_PREFIXES
