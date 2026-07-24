from __future__ import annotations

import hashlib
import hmac
import json
import re
from pathlib import Path
from typing import Final

from aws_purchase_scenario_models import JsonObject, JsonValue

COLLECTOR_ID: Final = "medikong.aws-live-fixture-attestation/v1"
MAX_ATTESTATION_AGE_SECONDS: Final = 300
MAX_ATTESTATION_FUTURE_SKEW_SECONDS: Final = 30
_INTEGRITY_PREFIX: Final = "hmac-sha256:"
_KEY_FINGERPRINT_PATTERN: Final = re.compile(r"sha256:[a-f0-9]{64}")
_MINIMUM_KEY_BYTES: Final = 32
_MAXIMUM_KEY_BYTES: Final = 4096


def read_attestation_key(path: Path) -> bytes | None:
    """Return bounded HMAC key bytes, or None when the key is unusable."""
    try:
        key = path.read_bytes()
    except OSError:
        return None
    if not _MINIMUM_KEY_BYTES <= len(key) <= _MAXIMUM_KEY_BYTES:
        return None
    return key


def attestation_key_matches_fingerprint(
    key: bytes,
    expected_fingerprint: str | None,
) -> bool:
    """Return whether key bytes match the control-plane trust anchor."""
    if (
        expected_fingerprint is None
        or _KEY_FINGERPRINT_PATTERN.fullmatch(expected_fingerprint) is None
    ):
        return False
    fingerprint = "sha256:" + hashlib.sha256(key).hexdigest()
    return hmac.compare_digest(fingerprint, expected_fingerprint)


def attestation_integrity(value: JsonObject, key: bytes) -> str:
    """Return the versioned HMAC for an attestation JSON object."""
    unsigned: dict[str, JsonValue] = {
        name: member for name, member in value.items() if name != "integrity"
    }
    canonical = json.dumps(
        unsigned,
        separators=(",", ":"),
        sort_keys=True,
    ).encode("utf-8")
    return _INTEGRITY_PREFIX + hmac.new(
        key,
        canonical,
        hashlib.sha256,
    ).hexdigest()


def integrity_matches(value: JsonObject, key: bytes) -> bool:
    """Return whether the supplied integrity field matches the artifact."""
    supplied = value.get("integrity")
    return type(supplied) is str and hmac.compare_digest(
        supplied,
        attestation_integrity(value, key),
    )
