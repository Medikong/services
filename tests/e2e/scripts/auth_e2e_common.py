from __future__ import annotations

import subprocess
from pathlib import Path


def generate_rsa_private_key(path: Path) -> None:
    """Generate the runtime-only RSA key shared by Auth Docker E2E lanes."""
    path.parent.mkdir(parents=True, exist_ok=True)
    subprocess.run(
        [
            "openssl",
            "genpkey",
            "-algorithm",
            "RSA",
            "-pkeyopt",
            "rsa_keygen_bits:2048",
            "-out",
            str(path),
        ],
        check=True,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.PIPE,
    )
    path.chmod(0o600)
