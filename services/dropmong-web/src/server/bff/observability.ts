import "server-only";

import { getConfig } from "@/server/bff/config";
import type { RequestContext } from "@/server/bff/request-context";

type RequestStats = {
  count: number;
  durationSeconds: number;
};

const requestStats = new Map<string, RequestStats>();

export function recordRequest(
  context: RequestContext,
  status: number,
  durationMs: number,
  downstreamService?: string,
): void {
  const config = getConfig();
  const statusClass = `${Math.floor(status / 100)}xx`;
  const key = `${context.method}\u0000${context.route}\u0000${statusClass}`;
  const existing = requestStats.get(key) ?? { count: 0, durationSeconds: 0 };
  existing.count += 1;
  existing.durationSeconds += durationMs / 1000;
  requestStats.set(key, existing);

  console.log(
    JSON.stringify({
      timestamp: new Date().toISOString(),
      level: status >= 500 ? "error" : "info",
      message: "web request completed",
      service: "dropmong-web",
      version: config.appVersion,
      environment: config.appEnvironment,
      request_id: context.requestId,
      trace_id: traceIdFromTraceparent(context.traceparent),
      route: context.route,
      method: context.method,
      status,
      duration_ms: durationMs,
      ...(downstreamService ? { downstream_service: downstreamService } : {}),
    }),
  );
}

export function metricsText(): string {
  const config = getConfig();
  const lines = [
    "# HELP http_requests_total Number of HTTP requests completed by dropmong-web.",
    "# TYPE http_requests_total counter",
  ];

  for (const [key, stats] of requestStats) {
    const [method, route, status] = key.split("\u0000");
    const labels = metricLabels({
      service: "dropmong-web",
      method,
      path: route,
      status,
    });
    lines.push(`http_requests_total{${labels}} ${stats.count}`);
    lines.push(`http_request_duration_seconds_sum{${labels}} ${stats.durationSeconds.toFixed(6)}`);
    lines.push(`http_request_duration_seconds_count{${labels}} ${stats.count}`);
  }

  lines.push("# HELP http_request_duration_seconds HTTP request duration in seconds.");
  lines.push("# TYPE http_request_duration_seconds histogram");
  lines.push("# HELP service_ready Service readiness state. Ready is 1, not ready is 0.");
  lines.push("# TYPE service_ready gauge");
  lines.push(
    `service_ready{${metricLabels({
      service_name: "dropmong-web",
      service_version: config.appVersion,
      service_environment: config.appEnvironment,
    })}} 1`,
  );
  return `${lines.join("\n")}\n`;
}

export function traceIdFromTraceparent(traceparent: string): string {
  return traceparent.split("-")[1] ?? "";
}

function metricLabels(values: Record<string, string>): string {
  return Object.entries(values)
    .map(([key, value]) => `${key}="${value.replaceAll("\\", "\\\\").replaceAll('"', '\\"')}"`)
    .join(",");
}
