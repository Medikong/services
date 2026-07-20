import { afterEach, describe, expect, it, vi } from "vitest";

import { beginRequest, metricsText, recordRequest } from "@/server/bff/observability";
import type { RequestContext } from "@/server/bff/request-context";

const context: RequestContext = {
  method: "POST",
  requestId: "request-observability-test",
  route: "/api/web/checkouts/[checkoutId]/confirm",
  traceparent: "00-4f3b2c1a9d8e7f60123456789abcdef0-6f1a2b3c4d5e6f70-01",
};

afterEach(() => {
  vi.restoreAllMocks();
});

describe("DropMong web observability contract", () => {
  it("exports the shared histogram and active-request labels", () => {
    vi.spyOn(console, "log").mockImplementation(() => undefined);

    beginRequest(context);
    expect(metricsText()).toContain(
      'http_server_active_requests{service_name="dropmong-web",service_version="test",service_environment="local",http_route="/api/web/checkouts/[checkoutId]/confirm",http_route_kind="api",http_request_method="POST"} 1',
    );

    recordRequest(context, 503, 250);
    const metrics = metricsText();
    expect(metrics).toContain("# TYPE http_server_request_duration_seconds histogram");
    expect(metrics).toContain('http_response_status_code="503"');
    expect(metrics).toContain('le="0.25"} 1');
    expect(metrics).toContain('le="+Inf"} 1');
    expect(metrics).toContain("http_server_request_duration_seconds_sum");
    expect(metrics).toContain("http_server_request_duration_seconds_count");
    expect(metrics).not.toContain("http_requests_total");
    expect(metrics).not.toContain("request_id");
    expect(metrics).not.toContain("trace_id");
  });

  it("writes the common metric-log-trace correlation fields", () => {
    const log = vi.spyOn(console, "log").mockImplementation(() => undefined);

    beginRequest(context);
    recordRequest(context, 200, 25);

    const event = JSON.parse(String(log.mock.calls.at(-1)?.[0])) as Record<string, unknown>;
    expect(event).toMatchObject({
      event: "http.request.completed",
      "service.name": "dropmong-web",
      "service.version": "test",
      "service.environment": "local",
      request_id: context.requestId,
      correlation_id: context.requestId,
      trace_id: "4f3b2c1a9d8e7f60123456789abcdef0",
      span_id: "6f1a2b3c4d5e6f70",
      "http.method": "POST",
      "http.route": context.route,
      "http.route.kind": "api",
      "http.status_code": 200,
      "log.kind": "access",
      "log.policy": "sample",
    });
  });

  it("bounds arbitrary request methods in metric labels", () => {
    vi.spyOn(console, "log").mockImplementation(() => undefined);
    const arbitraryMethods = ["X-CARDINALITY-1", "X-CARDINALITY-2"];

    for (const method of arbitraryMethods) {
      const arbitraryContext = { ...context, method, route: "/api/web/cardinality-test" };
      beginRequest(arbitraryContext);
      recordRequest(arbitraryContext, 405, 1);
    }

    const metrics = metricsText();
    expect(metrics).toContain('http_request_method="OTHER"');
    expect(metrics).not.toContain("X-CARDINALITY-1");
    expect(metrics).not.toContain("X-CARDINALITY-2");
  });
});
