from __future__ import annotations

from contextvars import ContextVar
from dataclasses import dataclass
import re
from uuid import uuid4

from middleware.types import ASGIApp, Receive, Scope, Send


REQUEST_ID_HEADER = "X-Request-Id"
CLIENT_ACTION_ID_HEADER = "X-Client-Action-Id"
REQUEST_ID_PATTERN = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]{0,127}")


@dataclass(frozen=True)
class RequestContext:
    """요청 처리 중 공통으로 사용하는 식별자 묶음."""

    request_id: str
    client_action_id: str | None = None


_request_context: ContextVar[RequestContext | None] = ContextVar("medikong_request_context", default=None)


class RequestContextMiddleware:
    """HTTP 요청마다 요청 컨텍스트를 만들고 ContextVar와 ASGI state에 보관한다.

    로그, 응답 헤더, 관측성 어댑터가 같은 request_id와 client_action_id를
    읽을 수 있도록 요청 처리 초기에 실행된다.
    """

    def __init__(
        self,
        app: ASGIApp,
        *,
        request_id_header: str = REQUEST_ID_HEADER,
        client_action_id_header: str = CLIENT_ACTION_ID_HEADER,
    ) -> None:
        self.app = app
        self.request_id_header = request_id_header
        self.client_action_id_header = client_action_id_header

    async def __call__(self, scope: Scope, receive: Receive, send: Send) -> None:
        # HTTP 요청이 아니면 요청 컨텍스트를 만들지 않고 다음 ASGI app으로 넘긴다.
        if scope["type"] != "http":
            await self.app(scope, receive, send)
            return

        # 요청 헤더에서 request_id와 client_action_id를 읽고, request_id가 없으면 새로 만든다.
        context = RequestContext(
            request_id=_header_or_generated(scope, self.request_id_header),
            client_action_id=_optional_header(scope, self.client_action_id_header),
        )

        # FastAPI/Starlette 코드가 request.state에서 같은 값을 읽을 수 있도록 ASGI state에 저장한다.
        state = scope.setdefault("state", {})
        state["request_id"] = context.request_id
        state["client_action_id"] = context.client_action_id

        # 뒤에 실행되는 middleware와 endpoint가 ContextVar로 현재 요청 식별자를 조회할 수 있게 설정한다.
        token = _request_context.set(context)
        try:
            await self.app(scope, receive, send)
        finally:
            # 요청 처리가 끝나면 ContextVar를 반드시 원래 상태로 되돌려 다른 요청에 섞이지 않게 한다.
            _request_context.reset(token)


def get_current_request_context() -> RequestContext | None:
    return _request_context.get()


def get_current_request_id() -> str | None:
    context = get_current_request_context()
    return context.request_id if context is not None else None


def get_current_client_action_id() -> str | None:
    context = get_current_request_context()
    return context.client_action_id if context is not None else None


def request_context_middleware_options() -> dict[str, str]:
    return {
        "request_id_header": REQUEST_ID_HEADER,
        "client_action_id_header": CLIENT_ACTION_ID_HEADER,
    }


def _header_or_generated(scope: Scope, header_name: str) -> str:
    value = _optional_header(scope, header_name)
    return value if value is not None and is_safe_request_id(value) else str(uuid4())


def is_safe_request_id(value: str) -> bool:
    normalized = value.strip()
    if REQUEST_ID_PATTERN.fullmatch(normalized) is None:
        return False
    lowered = normalized.lower()
    if lowered.startswith(("bearer", "card", "eyj", "token")):
        return False
    if normalized.isdigit() and 12 <= len(normalized) <= 19:
        return False
    return normalized.count(".") != 2


def _optional_header(scope: Scope, header_name: str) -> str | None:
    normalized = header_name.lower().encode("latin-1")
    for name, value in scope.get("headers", []):
        if name.lower() != normalized:
            continue
        decoded = value.decode("latin-1").strip()
        return decoded or None
    return None
