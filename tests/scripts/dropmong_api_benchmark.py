from __future__ import annotations

import argparse
import json
import time
from pathlib import Path
from typing import Any, Callable
from urllib.request import Request, urlopen


def main() -> None:
    parser = argparse.ArgumentParser(description="Run DropMong API smoke/large benchmarks.")
    parser.add_argument("--preset", default="smoke")
    parser.add_argument("--samples", type=int, default=5)
    parser.add_argument("--warmup", type=int, default=1)
    parser.add_argument("--reports-root", default="tests/tmp/reports/api-integration")
    parser.add_argument("--auth-url", default="http://127.0.0.1:18080")
    parser.add_argument("--user-url", default="http://127.0.0.1:18081")
    parser.add_argument("--coupon-url", default="http://127.0.0.1:18082")
    parser.add_argument("--backoffice-url", default="http://127.0.0.1:18083")
    args = parser.parse_args()

    urls = {key: getattr(args, f"{key}_url").rstrip("/") for key in ("auth", "user", "coupon", "backoffice")}
    operator = post_json(
        f"{urls['auth']}/internal/dev/test-token",
        {"token": f"bench-operator-{args.preset}", "userId": f"bench-operator-{args.preset}", "roles": ["operator"]},
    )
    post_json(
        f"{urls['backoffice']}/admin/drops/prepare",
        {
            "productId": f"bench-product-{args.preset}",
            "productName": "Benchmark Hoodie",
            "dropId": f"bench-drop-{args.preset}",
            "saleStartsAt": "2026-07-05T10:00:00Z",
            "stockQuantity": args.samples + args.warmup + 10,
            "couponPolicy": {"policyId": f"bench-policy-{args.preset}", "name": "Bench coupon", "totalQuantity": args.samples + args.warmup + 10},
        },
        principal_header(operator),
    )
    customer_tokens = [
        post_json(
            f"{urls['auth']}/internal/dev/test-token",
            {"token": f"bench-customer-{args.preset}-{index}", "userId": f"bench-customer-{args.preset}-{index}", "roles": ["customer"]},
        )
        for index in range(args.samples + args.warmup)
    ]

    endpoints = [
        measure("auth.test_token", args, lambda index: post_json(f"{urls['auth']}/internal/dev/test-token", {"token": f"bench-auth-{args.preset}-{index}", "userId": f"bench-auth-{args.preset}-{index}", "roles": ["customer"]})),
        measure("user.me", args, lambda index: get_json(f"{urls['user']}/users/me", principal_header(customer_tokens[index]))),
        measure("backoffice.readiness", args, lambda _index: get_json(f"{urls['backoffice']}/admin/drops/bench-drop-{args.preset}/readiness", principal_header(operator))),
        measure(
            "coupon.issue",
            args,
            lambda index: post_json(
                f"{urls['coupon']}/coupons/issue",
                {"policyId": f"bench-policy-{args.preset}"},
                {**principal_header(customer_tokens[index]), "Idempotency-Key": f"bench-coupon-{args.preset}-{index}"},
            ),
        ),
    ]
    artifact = {
        "preset": args.preset,
        "samples": args.samples,
        "warmup": args.warmup,
        "generatedAtEpoch": int(time.time()),
        "endpoints": endpoints,
    }
    out_dir = Path(args.reports_root) / "dropmong" / args.preset
    out_dir.mkdir(parents=True, exist_ok=True)
    (out_dir / "latest.json").write_text(json.dumps(artifact, indent=2), encoding="utf-8")
    (out_dir / "report.md").write_text(render_markdown(artifact), encoding="utf-8")
    print(out_dir / "report.md")


def measure(name: str, args: argparse.Namespace, call: Callable[[int], dict[str, Any]]) -> dict[str, Any]:
    for index in range(args.warmup):
        call(index)
    values = []
    for sample in range(args.samples):
        index = args.warmup + sample
        started = time.perf_counter()
        call(index)
        values.append((time.perf_counter() - started) * 1000)
    return {
        "endpoint": name,
        "samples": args.samples,
        "minMs": min(values),
        "p50Ms": percentile(values, 50),
        "p95Ms": percentile(values, 95),
        "p99Ms": percentile(values, 99),
        "maxMs": max(values),
    }


def percentile(values: list[float], pct: int) -> float:
    ordered = sorted(values)
    index = max(0, min(len(ordered) - 1, int((len(ordered) * pct + 99) / 100) - 1))
    return ordered[index]


def render_markdown(artifact: dict[str, Any]) -> str:
    lines = [
        "# DropMong API 벤치마크",
        "",
        f"- preset: `{artifact['preset']}`",
        f"- samples: `{artifact['samples']}`",
        f"- warmup: `{artifact['warmup']}`",
        "",
        "| endpoint | minMs | p50Ms | p95Ms | p99Ms | maxMs |",
        "| --- | ---: | ---: | ---: | ---: | ---: |",
    ]
    for row in artifact["endpoints"]:
        lines.append(
            f"| {row['endpoint']} | {row['minMs']:.3f} | {row['p50Ms']:.3f} | {row['p95Ms']:.3f} | {row['p99Ms']:.3f} | {row['maxMs']:.3f} |"
        )
    lines.extend(
        [
            "",
            "## 해석 기준",
            "",
            "- 이 벤치마크는 한 프로세스에서 순차 HTTP 호출로 API 1회 처리 비용을 보는 smoke 성격이다.",
            "- 대규모 부하와 HPA 판단은 `gitops` loadtest/k6 경로에서 별도로 다룬다.",
        ]
    )
    return "\n".join(lines) + "\n"


def principal_header(token_response: dict[str, Any]) -> dict[str, str]:
    return {"X-Principal": token_response["principalHeader"]}


def get_json(url: str, headers: dict[str, str] | None = None) -> dict[str, Any]:
    return request_json("GET", url, None, headers)


def post_json(url: str, payload: dict[str, Any], headers: dict[str, str] | None = None) -> dict[str, Any]:
    return request_json("POST", url, payload, headers)


def request_json(method: str, url: str, payload: dict[str, Any] | None, headers: dict[str, str] | None = None) -> dict[str, Any]:
    data = json.dumps(payload).encode("utf-8") if payload is not None else None
    request = Request(url, data=data, method=method, headers={"Content-Type": "application/json", **(headers or {})})
    with urlopen(request, timeout=10) as response:
        body = json.loads(response.read().decode("utf-8"))
        if not isinstance(body, dict):
            raise RuntimeError(f"expected object from {url}")
        return body


if __name__ == "__main__":
    main()
