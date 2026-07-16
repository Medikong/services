from __future__ import annotations

import asyncio
from collections.abc import Callable

import pytest
from redis import Redis
from redis.asyncio import Redis as AsyncRedis

from redisutil import (
    MAX_KEY_LENGTH,
    KeyBuilder,
    create_async_client,
    create_client,
)
from redisutil import client as client_module


def test_create_client_instruments_before_returning_native_client(monkeypatch) -> None:
    calls: list[str] = []

    def fake_instrument_redis(_provider: object = None) -> bool:
        calls.append("instrument")
        return True

    monkeypatch.setattr(client_module, "instrument_redis", fake_instrument_redis)

    client = create_client(
        "redis://localhost:6379/0",
        client_options={"decode_responses": True},
    )

    assert isinstance(client, Redis)
    assert calls == ["instrument"]
    assert client.connection_pool.connection_kwargs["decode_responses"] is True
    client.close()


def test_create_async_client_instruments_and_returns_native_client(monkeypatch) -> None:
    calls: list[str] = []

    def fake_instrument_redis(_provider: object = None) -> bool:
        calls.append("instrument")
        return True

    monkeypatch.setattr(client_module, "instrument_redis", fake_instrument_redis)

    client = create_async_client("redis://localhost:6379/0")

    assert isinstance(client, AsyncRedis)
    assert calls == ["instrument"]
    asyncio.run(client.aclose())


@pytest.mark.parametrize("factory", [create_client, create_async_client])
def test_client_factory_rejects_empty_url(factory: Callable[..., object]) -> None:
    with pytest.raises(ValueError, match="Redis URL is required"):
        factory("  ")


def test_key_builder_matches_shared_contract() -> None:
    builder = KeyBuilder("prod", "coupon-service", 1)

    assert (
        builder.build("campaign:admission", "user:1 / east")
        == "prod:coupon-service:v1:campaign%3Aadmission:user%3A1%20%2F%20east"
    )
    builder = KeyBuilder("prod", "coupon-service", 2)
    assert (
        builder.build_with_hash_tag(
            "campaign:123",
            "campaign-admission",
            "user/9",
        )
        == "prod:coupon-service:v2:{campaign%3A123}:campaign-admission:user%2F9"
    )


@pytest.mark.parametrize(
    ("build", "message"),
    [
        (lambda: KeyBuilder("", "coupon-service", 1), "environment"),
        (lambda: KeyBuilder("prod", "coupon:service", 1), "service"),
        (lambda: KeyBuilder("prod", "coupon", 0), "schema version"),
        (lambda: KeyBuilder("prod", "coupon", 1).build(), "identifier"),
        (lambda: KeyBuilder("prod", "coupon", 1).build(""), "identifier_0"),
        (
            lambda: KeyBuilder("prod", "coupon", 1).build_with_hash_tag("", "id"),
            "hash_tag",
        ),
        (
            lambda: KeyBuilder("prod", "coupon", 1).build("x" * MAX_KEY_LENGTH),
            "key length",
        ),
    ],
)
def test_key_builder_rejects_invalid_keys(
    build: Callable[[], object],
    message: str,
) -> None:
    with pytest.raises(ValueError, match=message):
        build()
