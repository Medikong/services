from __future__ import annotations

import subprocess
import sys
import unittest
from pathlib import Path


sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from run_e2e import AUTH_ADMIN_TOKEN, failure_message, readiness_description  # noqa: E402


class FailureMessageTests(unittest.TestCase):
    def test_omits_subprocess_command_arguments(self) -> None:
        sensitive_value = AUTH_ADMIN_TOKEN
        error = subprocess.CalledProcessError(
            17,
            ["newman", "--env-var", f"adminToken={sensitive_value}"],
        )

        message = failure_message(error)

        self.assertEqual(message, "subprocess exited with status 17")
        self.assertNotIn(sensitive_value, message)
        self.assertNotIn("adminToken", message)

    def test_readiness_description_allows_only_operational_status_fields(self) -> None:
        body = (
            b'{"status":"not_ready","checks":{"broker":"error","drain":"ok",'
            b'"unsafe-name":"secret-value"},"detail":"must-not-be-printed"}'
        )

        message = readiness_description(503, body)

        self.assertEqual(
            message,
            "http 503 status=not_ready checks=broker:error,drain:ok",
        )
        self.assertNotIn("secret", message)
        self.assertNotIn("detail", message)


if __name__ == "__main__":
    unittest.main()
