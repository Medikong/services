import "server-only";

import { getConfig } from "@/server/bff/config";
import { BffError } from "@/server/bff/errors";
import type { RequestContext } from "@/server/bff/request-context";
import type { DevelopmentSellerActor, SellerCommandResult, SellerPageData, SellerPageKind } from "@/server/bff/seller/contracts/types";
import { createSignedSellerScope } from "@/server/bff/seller/security";
import { getSellerPageFixture } from "@/server/bff/seller/clients/fixtures";

export async function readSellerPage(
  context: RequestContext,
  actor: DevelopmentSellerActor,
  kind: SellerPageKind,
  search: URLSearchParams,
): Promise<SellerPageData> {
  const config = getConfig();
  if (config.developmentMocks) return getSellerPageFixture(kind, search);
  if (!config.sellerManagementBaseUrl) throw unavailable();
  const url = new URL(`/web/seller/pages/${kind}`, config.sellerManagementBaseUrl);
  url.search = search.toString();
  const response = await fetch(url, {
    cache: "no-store",
    headers: { "x-seller-scope": createSignedSellerScope(actor), traceparent: context.traceparent, "x-request-id": context.requestId },
    signal: AbortSignal.timeout(4_000),
  }).catch(() => { throw unavailable(); });
  if (!response.ok) throw mapSellerDownstreamStatus(response.status);
  const value: unknown = await response.json();
  if (!isSellerPageData(value)) throw unavailable();
  return value;
}

export async function sendSellerCommand(
  context: RequestContext,
  actor: DevelopmentSellerActor,
  path: string,
  body: unknown,
  idempotencyKey: string,
  auditClientIp: string | null,
): Promise<SellerCommandResult> {
  const config = getConfig();
  if (config.developmentMocks) {
    return { message: "요청을 안전하게 처리했습니다.", operationId: `dev-${idempotencyKey}`, status: "completed", version: "result-v1" };
  }
  if (!config.sellerManagementBaseUrl) throw unavailable();
  const response = await fetch(new URL(`/web/seller/commands/${encodeURIComponent(path)}`, config.sellerManagementBaseUrl), {
    method: "POST", cache: "no-store", body: JSON.stringify(body),
    headers: {
      "content-type": "application/json", "idempotency-key": idempotencyKey,
      "x-seller-scope": createSignedSellerScope(actor), traceparent: context.traceparent, "x-request-id": context.requestId,
      ...(auditClientIp ? { "x-audit-client-ip": auditClientIp } : {}),
    }, signal: AbortSignal.timeout(6_000),
  }).catch(() => { throw unavailable(); });
  if (!response.ok) throw mapSellerDownstreamStatus(response.status);
  const value: unknown = await response.json();
  if (!isCommandResult(value)) throw unavailable();
  return value;
}

function unavailable(): BffError {
  return new BffError({ code: "WEB_SELLER_DEPENDENCY_UNAVAILABLE", message: "판매자 업무 시스템에 연결할 수 없습니다.", retryable: true, status: 503 });
}
export function mapSellerDownstreamStatus(status: number): BffError {
  if (status === 403) return new BffError({ code: "WEB_PERMISSION_DENIED", message: "이 작업을 수행할 권한이 없습니다.", status: 403 });
  if (status === 404) return new BffError({ code: "WEB_RESOURCE_NOT_FOUND", message: "요청한 정보를 찾을 수 없습니다.", status: 404 });
  if (status === 409) return new BffError({ code: "WEB_STATE_CONFLICT", message: "다른 작업에서 정보가 변경되었습니다. 새로고침해 주세요.", status: 409 });
  return unavailable();
}
function isSellerPageData(value: unknown): value is SellerPageData {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const item = value as Record<string, unknown>;
  return typeof item.title === "string" && Array.isArray(item.rows) && Array.isArray(item.columns) && typeof item.version === "string";
}
function isCommandResult(value: unknown): value is SellerCommandResult {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const item = value as Record<string, unknown>;
  return typeof item.message === "string" && typeof item.operationId === "string" && typeof item.version === "string"
    && (item.status === "accepted" || item.status === "completed");
}
