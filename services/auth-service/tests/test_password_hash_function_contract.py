import base64
import hashlib
import hmac
import json
import os
import platform
import statistics
import time

import pytest

from app.security import verify_password_legacy_pbkdf2


PASSWORD = "benchmark-password-1234"
WRONG_PASSWORD = "wrong-password-1234"
ITERATIONS = 210_000
SALT = b"medikong-auth-benchmark-salt"
EXPECTED_DIGEST_B64 = "8tYERV1b/ptbfLi8/TVwUxf46aJ5TxmBowZGazoNn70="
FIXTURE_HASH = (
    "pbkdf2_sha256$210000$bWVkaWtvbmctYXV0aC1iZW5jaG1hcmstc2FsdA=="
    "$8tYERV1b/ptbfLi8/TVwUxf46aJ5TxmBowZGazoNn70="
)


def test_pbkdf2_sha256_fixture_matches_go_contract() -> None:
    digest = hashlib.pbkdf2_hmac("sha256", PASSWORD.encode("utf-8"), SALT, ITERATIONS)

    assert base64.b64encode(digest).decode("ascii") == EXPECTED_DIGEST_B64
    assert verify_password_legacy_pbkdf2(PASSWORD, FIXTURE_HASH)
    assert not verify_password_legacy_pbkdf2(WRONG_PASSWORD, FIXTURE_HASH)


def test_pbkdf2_sha256_uses_constant_time_digest_compare() -> None:
    digest = hashlib.pbkdf2_hmac("sha256", PASSWORD.encode("utf-8"), SALT, ITERATIONS)
    expected = base64.b64decode(EXPECTED_DIGEST_B64.encode("ascii"))

    assert hmac.compare_digest(digest, expected)


def test_pbkdf2_sha256_function_benchmark_outputs_summary() -> None:
    if os.getenv("AUTH_FUNCTION_BENCHMARK") != "1":
        pytest.skip("set AUTH_FUNCTION_BENCHMARK=1 to run the function benchmark")

    samples = int(os.getenv("AUTH_FUNCTION_BENCHMARK_SAMPLES", "20"))
    if samples < 3:
        raise ValueError("AUTH_FUNCTION_BENCHMARK_SAMPLES must be at least 3")

    durations_ms: list[float] = []
    for _ in range(samples):
        started_at = time.perf_counter_ns()
        verified = verify_password_legacy_pbkdf2(PASSWORD, FIXTURE_HASH)
        elapsed_ms = (time.perf_counter_ns() - started_at) / 1_000_000
        if not verified:
            raise AssertionError("PBKDF2 benchmark password verification failed")
        durations_ms.append(elapsed_ms)

    print(
        json.dumps(
            {
                "algorithm": "pbkdf2_hmac_sha256",
                "environment": {
                    "language": "python",
                    "python": platform.python_version(),
                    "platform": platform.platform(),
                    "samples": samples,
                },
                "parameters": {
                    "iterations": ITERATIONS,
                    "salt_bytes": len(SALT),
                    "digest_bytes": 32,
                },
                "result": {
                    "mean_ms": round(statistics.fmean(durations_ms), 3),
                    "median_ms": round(statistics.median(durations_ms), 3),
                    "min_ms": round(min(durations_ms), 3),
                    "max_ms": round(max(durations_ms), 3),
                },
            },
            indent=2,
            sort_keys=True,
        )
    )
