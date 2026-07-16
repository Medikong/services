from __future__ import annotations

from dataclasses import dataclass
import re
from urllib.parse import quote


MAX_KEY_LENGTH = 512
_STRUCTURAL_SEGMENT = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]*$")


@dataclass(frozen=True, slots=True)
class KeyBuilder:
    """Build namespaced Redis keys without owning a Redis client.

    Example:
        >>> builder = KeyBuilder("local", "product-service", 1)
        >>> builder.build("catalog", "product/42")
        'local:product-service:v1:catalog:product%2F42'
        >>> builder.build_with_hash_tag(
        ...     "category:7", "catalog", "product/42"
        ... )
        'local:product-service:v1:{category%3A7}:catalog:product%2F42'
    """

    environment: str
    service: str
    schema_version: int

    def __post_init__(self) -> None:
        """Validate the immutable namespace used by every generated key."""
        _validate_structural_segment("environment", self.environment)
        _validate_structural_segment("service", self.service)
        if self.schema_version < 1:
            raise ValueError("schema version must be positive")

    def build(
        self,
        *identifiers: str,
    ) -> str:
        """Build a key using the shared environment/service/version contract.

        Args:
            identifiers: One or more dynamic key segments.

        Returns:
            A percent-encoded Redis key.

        Raises:
            ValueError: If a segment or the complete key is invalid.
        """
        if not identifiers:
            raise ValueError("at least one identifier is required")
        return self._build(None, identifiers)

    def build_with_hash_tag(
        self,
        hash_tag: str,
        *identifiers: str,
    ) -> str:
        """Build a key with an explicit Redis Cluster hash tag.

        Args:
            hash_tag: Dynamic segment used for Redis Cluster slot selection.
            identifiers: Additional dynamic key segments.

        Returns:
            A percent-encoded Redis key containing one explicit hash tag.

        Raises:
            ValueError: If a segment or the complete key is invalid.
        """
        encoded_hash_tag = _encode_identifier("hash_tag", hash_tag)
        return self._build(
            f"{{{encoded_hash_tag}}}",
            identifiers,
        )

    def _build(
        self,
        hash_tag: str | None,
        identifiers: tuple[str, ...],
    ) -> str:
        segments = [
            self.environment,
            self.service,
            f"v{self.schema_version}",
        ]
        if hash_tag is not None:
            segments.append(hash_tag)
        segments.extend(
            _encode_identifier(f"identifier_{index}", identifier)
            for index, identifier in enumerate(identifiers)
        )
        key = ":".join(segments)
        key_length = len(key.encode("utf-8"))
        if key_length > MAX_KEY_LENGTH:
            raise ValueError(
                f"key length {key_length} exceeds maximum {MAX_KEY_LENGTH}"
            )
        return key


def _validate_structural_segment(name: str, value: str) -> None:
    if not _STRUCTURAL_SEGMENT.fullmatch(value):
        raise ValueError(
            f"{name} must contain only letters, numbers, dot, underscore, or hyphen"
        )


def _encode_identifier(name: str, value: str) -> str:
    if not value:
        raise ValueError(f"{name} is required")
    return quote(value, safe="-._~")
