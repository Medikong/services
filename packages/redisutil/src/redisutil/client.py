from __future__ import annotations

from collections.abc import Mapping
from typing import Any

from opentelemetry.trace import TracerProvider
from redis import Redis
from redis.asyncio import Redis as AsyncRedis

from observability import instrument_redis


def create_client(
    url: str,
    *,
    tracer_provider: TracerProvider | None = None,
    client_options: Mapping[str, Any] | None = None,
) -> Redis:
    """Create an instrumented native synchronous redis-py client.

    Args:
        url: Redis or Valkey connection URL.
        tracer_provider: Optional provider used by Redis client spans.
        client_options: Native options forwarded to ``Redis.from_url``.

    Returns:
        A native ``redis.Redis`` client.

    Raises:
        ValueError: If the connection URL is empty or invalid.
    """
    _validate_url(url)
    instrument_redis(tracer_provider)
    return Redis.from_url(url, **dict(client_options or {}))


def create_async_client(
    url: str,
    *,
    tracer_provider: TracerProvider | None = None,
    client_options: Mapping[str, Any] | None = None,
) -> AsyncRedis:
    """Create an instrumented native asynchronous redis-py client.

    Args:
        url: Redis or Valkey connection URL.
        tracer_provider: Optional provider used by Redis client spans.
        client_options: Native options forwarded to ``AsyncRedis.from_url``.

    Returns:
        A native ``redis.asyncio.Redis`` client.

    Raises:
        ValueError: If the connection URL is empty or invalid.
    """
    _validate_url(url)
    instrument_redis(tracer_provider)
    return AsyncRedis.from_url(url, **dict(client_options or {}))


def _validate_url(url: str) -> None:
    if not url.strip():
        raise ValueError("Redis URL is required")
