from __future__ import annotations

import json
import subprocess
from dataclasses import dataclass
from pathlib import Path
from typing import Self
from urllib.request import Request

import pytest

import aws_purchase_live_fixture_proofs as proof_adapters
from aws_purchase_fixture_test_support import _valid_manifest

ATTESTATION_KEY = b"test-only-live-attestation-key-material"
SECRET_KEYS = (
    "SYNTHETIC_CUSTOMER_EMAIL",
    "SYNTHETIC_CUSTOMER_PASSWORD",
    "SYNTHETIC_CUSTOMER_B_EMAIL",
    "SYNTHETIC_CUSTOMER_B_PASSWORD",
)


@dataclass(frozen=True, slots=True)
class FakeProofs:
    catalog_payload: bytes
    order_output: str = "42|0|0|0\n"
    secret_output: str = "\n".join(SECRET_KEYS) + "\n"
    secret_return_code: int = 0
    secret_error: str = ""


@dataclass(frozen=True, slots=True)
class _FakeResponse:
    payload: bytes

    def __enter__(self) -> Self:
        return self

    def __exit__(
        self,
        exception_type: type[BaseException] | None,
        exception: BaseException | None,
        traceback: object | None,
    ) -> None:
        del exception_type, exception, traceback

    def read(self, amount: int) -> bytes:
        return self.payload[:amount]


def catalog_payload(*, status: str = "OPEN", stock: int = 42) -> bytes:
    manifest = _valid_manifest()
    return json.dumps(
        {
            "data": {
                "id": manifest["fixture"]["drop_ref"],
                "status": status,
                "products": [
                    {
                        "id": manifest["fixture"]["product_ref"],
                        "remainingQuantity": stock,
                    }
                ],
            }
        }
    ).encode()


def collector_arguments(tmp_path: Path) -> tuple[list[str], Path]:
    manifest_path = tmp_path / "fixture.json"
    manifest_path.write_text(json.dumps(_valid_manifest()), encoding="utf-8")
    key_path = tmp_path / "attestation.key"
    key_path.write_bytes(ATTESTATION_KEY)
    output_path = tmp_path / "live-attestation.json"
    return (
        [
            "--environment",
            "aws-dev",
            "--fixture-manifest",
            str(manifest_path),
            "--catalog-base-url",
            "https://catalog.example.test",
            "--kube-context",
            "aws-dev",
            "--order-namespace",
            "dropmong-order",
            "--order-db-pod",
            "postgres-order-0",
            "--order-db-container",
            "postgres",
            "--order-db-name",
            "order_service",
            "--secret-namespace",
            "synthetic",
            "--secret-name",
            "synthetic-traffic-credentials",
            "--customer-a-email-key",
            SECRET_KEYS[0],
            "--customer-a-password-key",
            SECRET_KEYS[1],
            "--customer-b-email-key",
            SECRET_KEYS[2],
            "--customer-b-password-key",
            SECRET_KEYS[3],
            "--attestation-key-file",
            str(key_path),
            "--output",
            str(output_path),
        ],
        output_path,
    )


def install_fakes(
    monkeypatch: pytest.MonkeyPatch,
    proofs: FakeProofs,
) -> tuple[list[Request], list[list[str]]]:
    requests: list[Request] = []
    commands: list[list[str]] = []

    def fake_urlopen(request: Request, *, timeout: float) -> _FakeResponse:
        assert timeout > 0
        requests.append(request)
        return _FakeResponse(proofs.catalog_payload)

    def fake_run(
        command: list[str],
        *,
        check: bool,
        capture_output: bool,
        text: bool,
        encoding: str,
        timeout: float,
    ) -> subprocess.CompletedProcess[str]:
        assert check is False
        assert capture_output is True
        assert text is True
        assert encoding == "utf-8"
        assert timeout > 0
        commands.append(command)
        if "exec" in command:
            return subprocess.CompletedProcess(
                command,
                0,
                stdout=proofs.order_output,
                stderr="",
            )
        return subprocess.CompletedProcess(
            command,
            proofs.secret_return_code,
            stdout=proofs.secret_output,
            stderr=proofs.secret_error,
        )

    monkeypatch.setattr(proof_adapters, "urlopen", fake_urlopen)
    monkeypatch.setattr(proof_adapters.subprocess, "run", fake_run)
    return requests, commands
