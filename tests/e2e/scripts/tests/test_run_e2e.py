from __future__ import annotations

import io
import subprocess
import sys
import unittest
from contextlib import redirect_stderr
from pathlib import Path
from unittest import mock


sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from run_e2e import (  # noqa: E402
    AUTH_ADMIN_TOKEN,
    auth_jwt_private_key,
    auth_runtime_diagnostics,
    compose_state_descriptions,
    failure_message,
    readiness_description,
    startup_log_descriptions,
)


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

    def test_runtime_diagnostics_allow_only_safe_state_and_event_fields(self) -> None:
        states = (
            '[{"Service":"auth-service","State":"exited","Health":"",'
            '"ExitCode":1,"Environment":"JWT=must-not-be-printed"},'
            '{"Service":"attacker","State":"running","Health":"healthy",'
            '"ExitCode":0,"secret":"must-not-be-printed"}]'
        )
        logs = "\n".join(
            [
                'auth-service-1 | {"service":"auth-service","msg":"config load failed",'
                '"error":"open /tmp/must-not-be-printed: permission denied"}',
                'auth-worker-1 | {"service":"auth-service-worker","msg":"worker starting",'
                '"token":"must-not-be-printed"}',
                'auth-service-1 | {"service":"auth-service","msg":"must-not-be-printed"}',
            ]
        )

        messages = [*compose_state_descriptions(states), *startup_log_descriptions(logs)]
        output = "; ".join(messages)

        self.assertEqual(
            messages,
            [
                "auth-service state=exited health=none exit=1",
                "auth-service event=config_load_failed reason=permission_denied",
                "auth-worker event=worker_starting",
            ],
        )
        self.assertNotIn("must-not-be-printed", output)
        self.assertNotIn("JWT", output)
        self.assertNotIn("token", output)

    def test_generated_key_is_read_only_inside_private_directory(self) -> None:
        def write_test_key(path: Path, *, mode: int) -> None:
            path.write_text("runtime-only-test-key", encoding="ascii")
            path.chmod(mode)

        with mock.patch.dict("os.environ", {"AUTH_E2E_JWT_PRIVATE_KEY_FILE": ""}):
            with mock.patch("run_e2e.generate_rsa_private_key", side_effect=write_test_key) as generate:
                with auth_jwt_private_key() as path:
                    self.assertEqual(path.stat().st_mode & 0o777, 0o444)
                    self.assertEqual(path.parent.stat().st_mode & 0o777, 0o700)
                    generate.assert_called_once_with(path, mode=0o444)

    def test_diagnostics_ignore_malformed_json_field_types(self) -> None:
        readiness = readiness_description(
            503,
            b'{"status":{},"checks":{"redis":{},"postgres":[]}}',
        )
        states = compose_state_descriptions(
            '[{"Service":{},"State":"running","Health":"healthy","ExitCode":0}]'
        )
        logs = startup_log_descriptions(
            'auth-service-1 | {"service":{},"msg":[],"error":"must-not-be-printed"}'
        )

        self.assertEqual(readiness, "http 503")
        self.assertEqual(states, [])
        self.assertEqual(logs, [])

    def test_runtime_diagnostics_timeout_has_constant_safe_output(self) -> None:
        output = io.StringIO()
        timeout = subprocess.TimeoutExpired(["docker", "compose"], 5)

        with mock.patch("run_e2e.subprocess.run", side_effect=timeout):
            with redirect_stderr(output):
                auth_runtime_diagnostics()

        self.assertEqual(output.getvalue().strip(), "[E2E] auth runtime diagnostics unavailable")


if __name__ == "__main__":
    unittest.main()
