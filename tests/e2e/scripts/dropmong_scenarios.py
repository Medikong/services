from __future__ import annotations

import json
import os
import sys
import threading
from dataclasses import dataclass
from typing import Any
from urllib.error import HTTPError
from urllib.request import Request, urlopen


@dataclass(frozen=True)
class Settings:
    auth_url: str
    user_url: str
    coupon_url: str
    backoffice_url: str


def main() -> int:
    settings = Settings(
        auth_url=os.getenv("E2E_AUTH_SERVICE_URL", "http://127.0.0.1:18080").rstrip("/"),
        user_url=os.getenv("E2E_USER_SERVICE_URL", "http://127.0.0.1:18081").rstrip("/"),
        coupon_url=os.getenv("E2E_COUPON_SERVICE_URL", "http://127.0.0.1:18082").rstrip("/"),
        backoffice_url=os.getenv("E2E_BACKOFFICE_SERVICE_URL", "http://127.0.0.1:18083").rstrip("/"),
    )
    operator = post_json(
        f"{settings.auth_url}/internal/dev/test-token",
        {"token": "operator-e2e", "userId": "operator-e2e", "roles": ["operator"]},
    )
    customer = post_json(
        f"{settings.auth_url}/internal/dev/test-token",
        {"token": "customer-e2e", "userId": "customer-e2e", "roles": ["customer"]},
    )

    expect_error(
        "wrong password",
        lambda: post_json(f"{settings.auth_url}/auth/login", {"email": "missing@example.com", "password": "bad"}),
        401,
        "auth.invalid_credentials",
    )
    me = get_json(f"{settings.user_url}/users/me", principal_header(customer))
    assert me["userId"] == "customer-e2e", me
    expect_error(
        "customer cannot prepare drop",
        lambda: post_json(f"{settings.backoffice_url}/admin/drops/prepare", prepare_payload("drop-denied", "policy-denied", 1), principal_header(customer)),
        403,
        "auth.forbidden",
    )
    expect_error(
        "coupon before preparation",
        lambda: post_json(f"{settings.coupon_url}/coupons/issue", {"policyId": "policy-missing"}, principal_header(customer)),
        422,
        "coupon.policy_not_found",
    )

    readiness = post_json(
        f"{settings.backoffice_url}/admin/drops/prepare",
        prepare_payload("drop-e2e", "policy-e2e", 2),
        principal_header(operator),
    )
    assert readiness["ready"] is True, readiness
    readiness = get_json(f"{settings.backoffice_url}/admin/drops/drop-e2e/readiness", principal_header(operator))
    assert readiness["ready"] is True, readiness

    issued = post_json(
        f"{settings.coupon_url}/coupons/issue",
        {"policyId": "policy-e2e"},
        {**principal_header(customer), "Idempotency-Key": "coupon-e2e-1"},
    )
    assert issued["result"] == "issued", issued
    duplicate = post_json(
        f"{settings.coupon_url}/coupons/issue",
        {"policyId": "policy-e2e"},
        {**principal_header(customer), "Idempotency-Key": "coupon-e2e-1"},
    )
    assert duplicate["result"] == "duplicate", duplicate

    second = post_json(
        f"{settings.auth_url}/internal/dev/test-token",
        {"token": "customer-e2e-2", "userId": "customer-e2e-2", "roles": ["customer"]},
    )
    post_json(f"{settings.coupon_url}/coupons/issue", {"policyId": "policy-e2e"}, {**principal_header(second), "Idempotency-Key": "coupon-e2e-2"})
    third = post_json(
        f"{settings.auth_url}/internal/dev/test-token",
        {"token": "customer-e2e-3", "userId": "customer-e2e-3", "roles": ["customer"]},
    )
    expect_error(
        "sold out",
        lambda: post_json(f"{settings.coupon_url}/coupons/issue", {"policyId": "policy-e2e"}, {**principal_header(third), "Idempotency-Key": "coupon-e2e-3"}),
        409,
        "coupon.sold_out",
    )

    post_json(f"{settings.backoffice_url}/admin/drops/prepare", prepare_payload("drop-race", "policy-race", 3), principal_header(operator))
    race_results = issue_concurrently(settings, "policy-race")
    if race_results.count("issued") != 3 or race_results.count("coupon.sold_out") != 7:
        raise AssertionError(f"unexpected race results: {race_results}")

    print(json.dumps({"status": "passed", "race": race_results}, ensure_ascii=False))
    return 0


def prepare_payload(drop_id: str, policy_id: str, quantity: int) -> dict[str, Any]:
    return {
        "productId": f"product-{drop_id}",
        "productName": "DropMong Limited Hoodie",
        "dropId": drop_id,
        "saleStartsAt": "2026-07-05T10:00:00Z",
        "stockQuantity": 10,
        "couponPolicy": {"policyId": policy_id, "name": "Launch coupon", "totalQuantity": quantity},
    }


def issue_concurrently(settings: Settings, policy_id: str) -> list[str]:
    results = [""] * 10

    def worker(index: int) -> None:
        token = post_json(
            f"{settings.auth_url}/internal/dev/test-token",
            {"token": f"race-{index}", "userId": f"race-user-{index}", "roles": ["customer"]},
        )
        try:
            result = post_json(
                f"{settings.coupon_url}/coupons/issue",
                {"policyId": policy_id},
                {**principal_header(token), "Idempotency-Key": f"race-{index}"},
            )
            results[index] = result["result"]
        except ScenarioHTTPError as exc:
            results[index] = exc.code

    threads = [threading.Thread(target=worker, args=(index,)) for index in range(10)]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()
    return results


def principal_header(token_response: dict[str, Any]) -> dict[str, str]:
    return {"X-Principal": token_response["principalHeader"]}


class ScenarioHTTPError(RuntimeError):
    def __init__(self, status: int, code: str, body: dict[str, Any]):
        super().__init__(f"HTTP {status} {code}: {body}")
        self.status = status
        self.code = code
        self.body = body


def expect_error(label: str, call, status: int, code: str) -> None:
    try:
        call()
    except ScenarioHTTPError as exc:
        if exc.status == status and exc.code == code:
            return
        raise AssertionError(f"{label}: got {exc.status}/{exc.code}, want {status}/{code}") from exc
    raise AssertionError(f"{label}: expected error {status}/{code}")


def get_json(url: str, headers: dict[str, str] | None = None) -> dict[str, Any]:
    return request_json("GET", url, None, headers)


def post_json(url: str, payload: dict[str, Any], headers: dict[str, str] | None = None) -> dict[str, Any]:
    return request_json("POST", url, payload, headers)


def request_json(method: str, url: str, payload: dict[str, Any] | None, headers: dict[str, str] | None = None) -> dict[str, Any]:
    data = json.dumps(payload).encode("utf-8") if payload is not None else None
    request = Request(url, data=data, method=method, headers={"Content-Type": "application/json", **(headers or {})})
    try:
        with urlopen(request, timeout=10) as response:
            body = json.loads(response.read().decode("utf-8"))
            if not isinstance(body, dict):
                raise RuntimeError(f"expected object from {url}")
            return body
    except HTTPError as exc:
        body = json.loads(exc.read().decode("utf-8"))
        code = body.get("error", {}).get("code", "")
        raise ScenarioHTTPError(exc.code, code, body) from exc


if __name__ == "__main__":
    raise SystemExit(main())
