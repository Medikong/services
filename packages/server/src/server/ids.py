from __future__ import annotations

import secrets
import time
import uuid
from threading import Lock
from uuid import UUID

from sqlalchemy import Uuid


DETERMINISTIC_UUID_NAMESPACE = UUID("018f0d5b-8e30-7a60-9bf1-91b6d979d3c0")

_uuid7_lock = Lock()
_uuid7_last_ms = -1
_uuid7_sequence = 0


def new_uuid_v7() -> UUID:
    stdlib_uuid7 = getattr(uuid, "uuid7", None)
    if callable(stdlib_uuid7):
        return stdlib_uuid7()

    unix_ts_ms = _next_uuid7_timestamp_ms()
    random_tail = secrets.randbits(62)
    value = (
        (unix_ts_ms & ((1 << 48) - 1)) << 80
        | 0x7 << 76
        | _uuid7_sequence << 64
        | 0b10 << 62
        | random_tail
    )
    return UUID(int=value)


def new_uuid_v7_string() -> str:
    return str(new_uuid_v7())


def deterministic_uuid(*parts: object, namespace: UUID = DETERMINISTIC_UUID_NAMESPACE) -> UUID:
    if not parts:
        raise ValueError("at least one deterministic UUID part is required")
    name = ":".join(str(part) for part in parts)
    return uuid.uuid5(namespace, name)


def deterministic_uuid_string(*parts: object, namespace: UUID = DETERMINISTIC_UUID_NAMESPACE) -> str:
    return str(deterministic_uuid(*parts, namespace=namespace))


def native_uuid() -> Uuid:
    return Uuid(as_uuid=False)


def _next_uuid7_timestamp_ms() -> int:
    global _uuid7_last_ms, _uuid7_sequence

    with _uuid7_lock:
        unix_ts_ms = time.time_ns() // 1_000_000
        if unix_ts_ms > _uuid7_last_ms:
            _uuid7_last_ms = unix_ts_ms
            _uuid7_sequence = secrets.randbits(12)
            return unix_ts_ms

        _uuid7_sequence = (_uuid7_sequence + 1) & 0x0FFF
        if _uuid7_sequence != 0:
            return _uuid7_last_ms

        while True:
            unix_ts_ms = time.time_ns() // 1_000_000
            if unix_ts_ms > _uuid7_last_ms:
                _uuid7_last_ms = unix_ts_ms
                _uuid7_sequence = secrets.randbits(12)
                return unix_ts_ms
