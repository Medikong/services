from __future__ import annotations

import socket
import subprocess
import sys
import time
from collections.abc import Iterator
from contextlib import contextmanager

import httpx
from fastapi.testclient import TestClient

from app.password_bench_api import app


def test_password_bench_api_contract() -> None:
    with TestClient(app) as client:
        response = client.post("/bench/password/verify", json={"password": "benchmark-password-1234"})

    assert response.status_code == 200
    assert response.json() == {
        "verified": True,
        "algorithm": "pbkdf2_sha256",
        "iterations": 210000,
    }


def test_password_bench_api_rejects_wrong_password() -> None:
    with TestClient(app) as client:
        response = client.post("/bench/password/verify", json={"password": "wrong-password-1234"})

    assert response.status_code == 200
    assert response.json()["verified"] is False


def test_password_bench_api_runs_through_uvicorn() -> None:
    with _uvicorn_server() as base_url:
        health = httpx.get(f"{base_url}/health", timeout=5)
        response = httpx.post(
            f"{base_url}/bench/password/verify",
            json={"password": "benchmark-password-1234"},
            timeout=10,
        )

    assert health.status_code == 200
    assert response.status_code == 200
    assert response.json()["verified"] is True


@contextmanager
def _uvicorn_server() -> Iterator[str]:
    port = _free_port()
    process = subprocess.Popen(
        [
            sys.executable,
            "-m",
            "uvicorn",
            "app.password_bench_api:app",
            "--host",
            "127.0.0.1",
            "--port",
            str(port),
            "--workers",
            "1",
            "--log-level",
            "warning",
        ],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    try:
        base_url = f"http://127.0.0.1:{port}"
        _wait_for_server(base_url)
        yield base_url
    finally:
        process.terminate()
        try:
            process.communicate(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.communicate(timeout=5)


def _wait_for_server(base_url: str) -> None:
    deadline = time.monotonic() + 10
    last_error: Exception | None = None
    while time.monotonic() < deadline:
        try:
            response = httpx.get(f"{base_url}/health", timeout=1)
            if response.status_code == 200:
                return
        except httpx.HTTPError as exc:
            last_error = exc
        time.sleep(0.1)
    raise AssertionError(f"uvicorn benchmark server did not start: {last_error}")


def _free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])
