from uuid import UUID

import pytest

from server.ids import deterministic_uuid_string, native_uuid, new_uuid_v7, new_uuid_v7_string


def test_new_uuid_v7_returns_version_7_uuid() -> None:
    generated = new_uuid_v7()

    assert generated.version == 7
    assert UUID(new_uuid_v7_string()).version == 7


def test_new_uuid_v7_is_orderable_by_uuid_value() -> None:
    generated = [new_uuid_v7() for _ in range(20)]

    assert generated == sorted(generated)


def test_deterministic_uuid_string_is_stable() -> None:
    first = deterministic_uuid_string("concert", 1)
    second = deterministic_uuid_string("concert", 1)
    different = deterministic_uuid_string("concert", 2)

    assert UUID(first).version == 5
    assert first == second
    assert first != different


def test_deterministic_uuid_requires_parts() -> None:
    with pytest.raises(ValueError, match="at least one"):
        deterministic_uuid_string()


def test_native_uuid_uses_string_orm_values() -> None:
    column_type = native_uuid()

    assert column_type.as_uuid is False
