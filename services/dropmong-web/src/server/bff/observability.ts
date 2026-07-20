import "server-only";

import { getConfig } from "@/server/bff/config";
import type { RequestContext } from "@/server/bff/request-context";

type HistogramStats = {
  buckets: number[];
  count: number;
  sum: number;
};

const durationBuckets = [0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10] as const;
const boundedMethods = new Set(["GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "CONNECT", "OPTIONS", "TRACE"]);
const requestDuration = new Map<string, HistogramStats>();
const activeRequests = new Map<string, number>();

export function beginRequest(context: RequestContext): void {
  const key = activeKey(context);
  activeRequests.set(key, (activeRequests.get(key) ?? 0) + 1);
}

export function recordRequest(
  context: RequestContext,
  status: number,
  durationMs: number,
  downstreamService?: string,
): void {
  const config = getConfig();
  const active = activeKey(context);
  activeRequests.set(active, Math.max(0, (activeRequests.get(active) ?? 1) - 1));

  const kind = routeKind(context.route);
  const key = [boundedMetricMethod(context.method), context.route, kind, String(status)].join("\u0000");
  const durationSeconds = durationMs / 1000;
  const stats = requestDuration.get(key) ?? {
    buckets: durationBuckets.map(() => 0),
    count: 0,
    sum: 0,
  };
  stats.count += 1;
  stats.sum += durationSeconds;
  durationBuckets.forEach((upperBound, index) => {
    if (durationSeconds <= upperBound) {
      stats.buckets[index] += 1;
    }
  });
  requestDuration.set(key, stats);

  const severity = status >= 500 ? "ERROR" : status >= 400 || durationMs >= 1000 ? "WARN" : "INFO";
  console.log(
    JSON.stringify({
      timestamp: new Date().toISOString(),
      level: severity.toLowerCase(),
      event: "http.request.completed",
      "service.name": "dropmong-web",
      "service.version": config.appVersion,
      "service.environment": config.appEnvironment,
      severity,
      severity_text: severity,
      request_id: context.requestId,
      correlation_id: context.requestId,
      trace_id: traceIdFromTraceparent(context.traceparent),
      span_id: spanIdFromTraceparent(context.traceparent),
      "http.method": context.method,
      "http.route": context.route,
      "http.route.kind": kind,
      "http.status_code": status,
      duration_ms: durationMs,
      "http.request.is_probe": kind === "probe",
      "log.kind": "access",
      "log.policy": logPolicy(kind, status, durationMs),
      ...(downstreamService ? { downstream_service: downstreamService } : {}),
    }),
  );
}

export function metricsText(): string {
  const config = getConfig();
  const identity = {
    service_name: "dropmong-web",
    service_version: config.appVersion,
    service_environment: config.appEnvironment,
  };
  const lines = [
    "# HELP http_server_request_duration_seconds HTTP server request duration in seconds.",
    "# TYPE http_server_request_duration_seconds histogram",
  ];

  for (const [key, stats] of requestDuration) {
    const [method, route, kind, status] = key.split("\u0000");
    const labels = {
      ...identity,
      http_route: route,
      http_route_kind: kind,
      http_request_method: method,
      http_response_status_code: status,
    };
    durationBuckets.forEach((upperBound, index) => {
      lines.push(
        `http_server_request_duration_seconds_bucket{${metricLabels({ ...labels, le: String(upperBound) })}} ${stats.buckets[index]}`,
      );
    });
    lines.push(
      `http_server_request_duration_seconds_bucket{${metricLabels({ ...labels, le: "+Inf" })}} ${stats.count}`,
      `http_server_request_duration_seconds_sum{${metricLabels(labels)}} ${stats.sum.toFixed(6)}`,
      `http_server_request_duration_seconds_count{${metricLabels(labels)}} ${stats.count}`,
    );
  }

  lines.push(
    "# HELP http_server_active_requests Currently active HTTP server requests.",
    "# TYPE http_server_active_requests gauge",
  );
  for (const [key, count] of activeRequests) {
    const [method, route, kind] = key.split("\u0000");
    lines.push(
      `http_server_active_requests{${metricLabels({
        ...identity,
        http_route: route,
        http_route_kind: kind,
        http_request_method: method,
      })}} ${count}`,
    );
  }

  lines.push(
    "# HELP service_ready Service readiness state. Ready is 1, not ready is 0.",
    "# TYPE service_ready gauge",
    `service_ready{${metricLabels(identity)}} 1`,
  );
  return `${lines.join("\n")}\n`;
}

export function traceIdFromTraceparent(traceparent: string): string {
  return traceparent.split("-")[1] ?? "";
}

export function spanIdFromTraceparent(traceparent: string): string {
  return traceparent.split("-")[2] ?? "";
}

function activeKey(context: RequestContext): string {
  return [boundedMetricMethod(context.method), context.route, routeKind(context.route)].join("\u0000");
}

function boundedMetricMethod(method: string): string {
  const normalized = method.trim().toUpperCase();
  return boundedMethods.has(normalized) ? normalized : "OTHER";
}

function routeKind(route: string): "api" | "probe" | "debug" | "unmatched" {
  if (["/healthz", "/readyz", "/metrics"].includes(route)) {
    return "probe";
  }
  if (route.startsWith("/debug") || route.startsWith("/_debug") || route.startsWith("/__debug")) {
    return "debug";
  }
  if (route === "unmatched") {
    return "unmatched";
  }
  return "api";
}

function logPolicy(kind: ReturnType<typeof routeKind>, status: number, durationMs: number): "drop" | "keep" | "sample" {
  if (kind === "probe" && status < 500) {
    return "drop";
  }
  if (status >= 400 || durationMs >= 1000 || kind === "debug") {
    return "keep";
  }
  return "sample";
}

function metricLabels(values: Record<string, string>): string {
  return Object.entries(values)
    .map(([key, value]) => `${key}="${value.replaceAll("\\", "\\\\").replaceAll("\n", "\\n").replaceAll('"', '\\"')}"`)
    .join(",");
}
