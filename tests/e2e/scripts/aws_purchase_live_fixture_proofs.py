from __future__ import annotations

import json
import subprocess
from dataclasses import dataclass
from enum import StrEnum, unique
from typing import Final
from urllib.error import HTTPError, URLError
from urllib.parse import quote, urlsplit, urlunsplit
from urllib.request import Request, urlopen

from aws_purchase_fixture_contract import _Manifest

_MAX_CATALOG_BYTES: Final = 1_048_576
_ORDER_QUERY: Final = """
SELECT i.total_quantity, i.reserved_quantity, i.sold_quantity,
       (SELECT COUNT(*) FROM orders o
        WHERE o.drop_id = :'drop_id' AND o.product_id = :'product_id')
FROM inventory_items i
WHERE i.drop_id = :'drop_id' AND i.product_id = :'product_id';
""".strip()
_SECRET_KEY_TEMPLATE: Final = (
    'go-template={{range $key, $_ := .data}}{{printf "%s\\n" $key}}{{end}}'
)


@unique
class _ProofReason(StrEnum):
    CATALOG_BLOCKED = "CATALOG_PROOF_BLOCKED"
    ORDER_BLOCKED = "ORDER_PROOF_BLOCKED"
    SECRET_BLOCKED = "SECRET_PROOF_BLOCKED"


@dataclass(frozen=True, slots=True)
class _LiveProofBlocked(Exception):
    reason: _ProofReason

    def __str__(self) -> str:
        return self.reason.value


@dataclass(frozen=True, slots=True)
class _LiveProofConfig:
    catalog_base_url: str
    kubectl: str
    kube_context: str
    order_namespace: str
    order_db_pod: str
    order_db_container: str | None
    order_db_name: str
    secret_namespace: str
    secret_name: str
    secret_keys: tuple[str, str, str, str]
    timeout_seconds: float


def _verify_catalog(config: _LiveProofConfig, manifest: _Manifest) -> None:
    parsed = urlsplit(config.catalog_base_url)
    if (
        parsed.scheme not in {"http", "https"}
        or parsed.hostname is None
        or parsed.username is not None
        or parsed.password is not None
        or parsed.path not in {"", "/"}
        or parsed.query
        or parsed.fragment
    ):
        raise _LiveProofBlocked(_ProofReason.CATALOG_BLOCKED)
    origin = urlunsplit((parsed.scheme, parsed.netloc, "", "", ""))
    url = f"{origin}/drops/{quote(manifest.fixture.drop_ref, safe='')}"
    try:
        with urlopen(  # noqa: S310 - validated http(s) control-plane origin.
            Request(url, method="GET"),
            timeout=config.timeout_seconds,
        ) as response:
            raw = response.read(_MAX_CATALOG_BYTES + 1)
        if len(raw) > _MAX_CATALOG_BYTES:
            raise _LiveProofBlocked(_ProofReason.CATALOG_BLOCKED)
        root = json.loads(raw)
    except (
        HTTPError,
        URLError,
        TimeoutError,
        OSError,
        UnicodeError,
        json.JSONDecodeError,
    ) as error:
        raise _LiveProofBlocked(_ProofReason.CATALOG_BLOCKED) from error
    if type(root) is not dict or type(root.get("data")) is not dict:
        raise _LiveProofBlocked(_ProofReason.CATALOG_BLOCKED)
    data = root["data"]
    products = data.get("products")
    if (
        data.get("id") != manifest.fixture.drop_ref
        or data.get("status") != "OPEN"
        or type(products) is not list
    ):
        raise _LiveProofBlocked(_ProofReason.CATALOG_BLOCKED)
    matching = [
        product
        for product in products
        if type(product) is dict
        and product.get("id") == manifest.fixture.product_ref
        and product.get("remainingQuantity") == manifest.fixture.stock
    ]
    if len(matching) != 1:
        raise _LiveProofBlocked(_ProofReason.CATALOG_BLOCKED)


def _kubectl(
    config: _LiveProofConfig,
    command: list[str],
    reason: _ProofReason,
) -> str:
    try:
        result = subprocess.run(
            [config.kubectl, "--context", config.kube_context, *command],
            check=False,
            capture_output=True,
            text=True,
            encoding="utf-8",
            timeout=config.timeout_seconds,
        )
    except (OSError, subprocess.TimeoutExpired, UnicodeError) as error:
        raise _LiveProofBlocked(reason) from error
    if result.returncode != 0:
        raise _LiveProofBlocked(reason)
    return result.stdout


def _verify_order(config: _LiveProofConfig, manifest: _Manifest) -> None:
    command = [
        "-n",
        config.order_namespace,
        "exec",
        config.order_db_pod,
    ]
    if config.order_db_container is not None:
        command.extend(("-c", config.order_db_container))
    command.extend(
        (
            "--",
            "psql",
            "--no-psqlrc",
            "-X",
            "-qAt",
            "-F",
            "|",
            "-v",
            "ON_ERROR_STOP=1",
            "-v",
            f"drop_id={manifest.fixture.drop_ref}",
            "-v",
            f"product_id={manifest.fixture.product_ref}",
            "-d",
            config.order_db_name,
            "-c",
            _ORDER_QUERY,
        )
    )
    if _kubectl(config, command, _ProofReason.ORDER_BLOCKED).strip() != "42|0|0|0":
        raise _LiveProofBlocked(_ProofReason.ORDER_BLOCKED)


def _verify_secret(config: _LiveProofConfig) -> None:
    output = _kubectl(
        config,
        [
            "-n",
            config.secret_namespace,
            "get",
            "secret",
            config.secret_name,
            "-o",
            _SECRET_KEY_TEMPLATE,
        ],
        _ProofReason.SECRET_BLOCKED,
    )
    names = frozenset(line.strip() for line in output.splitlines() if line.strip())
    if not set(config.secret_keys).issubset(names):
        raise _LiveProofBlocked(_ProofReason.SECRET_BLOCKED)


def verify_live_proofs(
    config: _LiveProofConfig,
    manifest: _Manifest,
) -> None:
    """Verify the catalog, authoritative inventory, and Secret key names."""
    _verify_catalog(config, manifest)
    _verify_order(config, manifest)
    _verify_secret(config)
