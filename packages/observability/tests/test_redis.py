from __future__ import annotations

import asyncio
from types import SimpleNamespace

from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import SimpleSpanProcessor
from opentelemetry.sdk.trace.export.in_memory_span_exporter import (
    InMemorySpanExporter,
)
from opentelemetry.trace import SpanKind, StatusCode
import pytest
from redis import Redis
from redis.asyncio import Redis as AsyncRedis
from redis.exceptions import RedisError

from observability import redis as redis_module


class _RecordingSpan:
    def __init__(self, name: str) -> None:
        self.name = name
        self.attributes: dict[str, str] = {}

    def is_recording(self) -> bool:
        return True

    def set_attribute(self, name: str, value: str) -> None:
        self.attributes[name] = value


def test_instrument_redis_registers_once_with_sanitizing_hooks(monkeypatch) -> None:
    calls: list[dict[str, object]] = []

    class FakeRedisInstrumentor:
        is_instrumented_by_opentelemetry = False

        def instrument(self, **kwargs: object) -> None:
            calls.append(kwargs)

    monkeypatch.setattr(redis_module, "RedisInstrumentor", FakeRedisInstrumentor)
    monkeypatch.setattr(redis_module, "_redis_instrumented", False)
    monkeypatch.setattr(redis_module, "_pipeline_metadata_sanitized", True)

    assert redis_module.instrument_redis()
    assert not redis_module.instrument_redis()
    assert len(calls) == 1
    assert calls[0]["request_hook"] is redis_module._sanitize_request


def test_instrument_redis_rejects_preexisting_automatic_instrumentation(
    monkeypatch,
) -> None:
    class FakeRedisInstrumentor:
        is_instrumented_by_opentelemetry = True

        def instrument(self, **_kwargs: object) -> None:
            raise AssertionError("instrument must not be called twice")

    monkeypatch.setattr(redis_module, "RedisInstrumentor", FakeRedisInstrumentor)
    monkeypatch.setattr(redis_module, "_redis_instrumented", False)

    with pytest.raises(RuntimeError, match="already active"):
        redis_module.instrument_redis()


def test_redis_hook_replaces_command_arguments() -> None:
    command_span = _RecordingSpan("redis.set")
    redis_module._sanitize_request(
        command_span,  # type: ignore[arg-type]
        object(),
        ["SET", "secret:key", "secret-value"],
        {},
    )
    assert command_span.attributes == {
        "db.statement": "SET",
        "db.query.text": "SET",
    }


def test_pipeline_metadata_never_contains_command_arguments(monkeypatch) -> None:
    def original(_instance: object) -> tuple[list[object], str, str]:
        return (
            [object(), object()],
            "SET secret:key secret-value\nGET secret:key",
            "redis.pipeline SET GET",
        )

    monkeypatch.setattr(
        redis_module.redis_instrumentation,
        "_build_span_meta_data_for_pipeline",
        original,
    )
    monkeypatch.setattr(redis_module, "_pipeline_metadata_sanitized", False)

    redis_module._sanitize_pipeline_metadata()
    command_stack, resource, span_name = (
        redis_module.redis_instrumentation._build_span_meta_data_for_pipeline(object())
    )

    assert len(command_stack) == 2
    assert resource == "PIPELINE"
    assert span_name == "redis.pipeline SET GET"


def test_sanitize_request_accepts_bytes_and_unknown_commands() -> None:
    bytes_span = _RecordingSpan("redis.get")
    redis_module._sanitize_request(
        bytes_span,  # type: ignore[arg-type]
        object(),
        [b"get", b"secret:key"],
        {},
    )
    assert bytes_span.attributes["db.statement"] == "GET"

    unknown_span = _RecordingSpan("redis.command")
    redis_module._sanitize_request(
        unknown_span,  # type: ignore[arg-type]
        object(),
        [SimpleNamespace()],
        {},
    )
    assert unknown_span.attributes["db.statement"] == "REDIS"


def test_real_instrumentation_preserves_context_errors_and_sanitization() -> None:
    exporter = InMemorySpanExporter()
    provider = TracerProvider()
    provider.add_span_processor(SimpleSpanProcessor(exporter))
    tracer = provider.get_tracer("redis-instrumentation-test")

    redis_module.instrument_redis(provider)
    try:
        with tracer.start_as_current_span("sync-parent") as sync_parent:
            sync_parent_id = sync_parent.get_span_context().span_id
            client = Redis.from_url(
                "redis://127.0.0.1:1/0",
                socket_connect_timeout=0.01,
                socket_timeout=0.01,
            )
            with pytest.raises(RedisError):
                client.evalsha(
                    "secret-sha",
                    1,
                    "secret:key",
                    "secret-value",
                )
            pipeline = client.pipeline()
            pipeline.set("pipeline:key", "pipeline-secret")
            pipeline.get("pipeline:key")
            with pytest.raises(RedisError):
                pipeline.execute()
            client.close()

        async_parent_id = asyncio.run(_run_async_error(tracer))
    finally:
        RedisInstrumentor = redis_module.RedisInstrumentor
        RedisInstrumentor().uninstrument()
        redis_module._redis_instrumented = False
        provider.shutdown()

    client_spans = [
        span for span in exporter.get_finished_spans() if span.kind is SpanKind.CLIENT
    ]
    assert {span.name for span in client_spans} == {"EVALSHA", "SET GET", "GET"}
    assert all(span.status.status_code is StatusCode.ERROR for span in client_spans)

    expected_parents = {
        "EVALSHA": sync_parent_id,
        "SET GET": sync_parent_id,
        "GET": async_parent_id,
    }
    for span in client_spans:
        assert span.parent is not None
        assert span.parent.span_id == expected_parents[span.name]
        exported = f"{span.name} {dict(span.attributes)}"
        for secret in (
            "secret-sha",
            "secret:key",
            "secret-value",
            "pipeline:key",
            "pipeline-secret",
            "async:secret",
        ):
            assert secret not in exported

    pipeline_span = next(span for span in client_spans if span.name == "SET GET")
    assert pipeline_span.attributes["db.statement"] == "PIPELINE"


async def _run_async_error(tracer) -> int:
    with tracer.start_as_current_span("async-parent") as parent:
        parent_id = parent.get_span_context().span_id
        client = AsyncRedis.from_url(
            "redis://127.0.0.1:1/0",
            socket_connect_timeout=0.01,
            socket_timeout=0.01,
        )
        with pytest.raises(RedisError):
            await client.get("async:secret")
        await client.aclose()
    return parent_id
