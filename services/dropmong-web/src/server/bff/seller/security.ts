import "server-only";

import { createHmac, randomBytes, randomUUID, timingSafeEqual } from "node:crypto";
import { isIP } from "node:net";

import { getConfig } from "@/server/bff/config";
import { BffError, RecentAuthRequiredError } from "@/server/bff/errors";
import type {
  DevelopmentSellerActor,
  SellerPermission,
  SellerScopeClaims,
} from "@/server/bff/seller/contracts/types";

export const developmentSellerSessionCookieName = "dropmong_dev_seller_session";
const allSellerPermissions: SellerPermission[] = [
  "seller.dashboard.read", "seller.drop.read", "seller.drop.write", "seller.drop.review.read",
  "seller.drop.review.submit", "seller.product.read", "seller.product.write", "seller.order.read",
  "seller.order.export", "seller.coupon.read", "seller.coupon.write", "seller.analytics.read",
  "seller.settlement.read", "seller.account.read", "seller.account.write", "seller.store.read",
  "seller.store.write", "seller.member.read", "seller.member.write", "seller.role.permission.write",
  "seller.issue.read", "seller.issue.write",
];

export function createDevelopmentSellerActor(mode: "active" | "onboarding" | "restricted" = "active"): DevelopmentSellerActor {
  const permissions = mode === "restricted"
    ? allSellerPermissions.filter((permission) => permission.endsWith(".read"))
    : allSellerPermissions;
  return {
    csrfToken: randomBytes(24).toString("base64url"),
    expiresAt: Date.now() + 1000 * 60 * 60 * 2,
    membership: mode === "onboarding" ? null : {
      id: "membership-seller-001",
      sellerId: "seller-001",
      version: "membership-v4",
      permissionVersion: "permissions-v7",
      roleLabel: mode === "restricted" ? "성과 조회자" : "대표 관리자",
      status: "ACTIVE",
      permissions,
    },
    onboardingAllowed: mode === "onboarding",
    recentAuthPurposes: [],
    sessionId: randomUUID(),
    userId: mode === "onboarding" ? "development-onboarding-001" : "development-seller-user-001",
  };
}

export function rotateDevelopmentSellerAuth(actor: DevelopmentSellerActor, purpose: string): DevelopmentSellerActor {
  return {
    ...actor,
    csrfToken: randomBytes(24).toString("base64url"),
    recentAuthPurposes: [...new Set([...actor.recentAuthPurposes, purpose])],
    sessionId: randomUUID(),
  };
}

export function signDevelopmentSellerSession(actor: DevelopmentSellerActor): string {
  return signJson(actor, sessionSigningKey());
}

export function readDevelopmentSellerActor(cookieValue: string | undefined): DevelopmentSellerActor | null {
  if (!cookieValue || !getConfig().developmentMocks) return null;
  const value = verifySignedJson(cookieValue, sessionSigningKey());
  return isDevelopmentSellerActor(value) && value.expiresAt > Date.now() ? value : null;
}

export function assertSellerMutation(request: Request, actor: DevelopmentSellerActor): string {
  const origin = request.headers.get("origin");
  if (!origin || origin !== getConfig().appOrigin.origin) {
    throw new BffError({ code: "WEB_CSRF_INVALID", message: "요청 출처를 확인할 수 없습니다.", status: 403 });
  }
  const received = request.headers.get("x-csrf-token");
  if (!received || !constantTimeEquals(received, actor.csrfToken)) {
    throw new BffError({ code: "WEB_CSRF_INVALID", message: "보안 확인이 만료되었습니다. 페이지를 새로고침해 주세요.", status: 403 });
  }
  const key = request.headers.get("idempotency-key")?.trim();
  if (!key || key.length > 128 || !/^[A-Za-z0-9._:-]+$/.test(key)) {
    throw new BffError({ code: "WEB_REQUEST_INVALID", message: "요청 식별자가 올바르지 않습니다.", status: 400 });
  }
  return key;
}

export function assertSellerPermission(actor: DevelopmentSellerActor, permission: SellerPermission): void {
  if (!actor.membership || actor.membership.status !== "ACTIVE" || !actor.membership.permissions.includes(permission)) {
    throw new BffError({ code: "WEB_PERMISSION_DENIED", message: "이 작업을 수행할 권한이 없습니다.", status: 403 });
  }
}

export function assertRecentSellerAuth(actor: DevelopmentSellerActor, purpose: string, returnTo: string): void {
  if (actor.recentAuthPurposes.includes(purpose)) return;
  const safeReturnTo = normalizeSellerReturnTo(returnTo);
  const token = signSellerReturnTo(safeReturnTo);
  throw new RecentAuthRequiredError(`/api/web/auth/development-seller-reauth?purpose=${encodeURIComponent(purpose)}&returnToken=${encodeURIComponent(token)}`);
}

export function assertSellerResourceVisible(resourceSellerId: string | null, actor: DevelopmentSellerActor): void {
  if (!resourceSellerId || resourceSellerId !== actor.membership?.sellerId) {
    throw new BffError({ code: "WEB_RESOURCE_NOT_FOUND", message: "요청한 정보를 찾을 수 없습니다.", status: 404 });
  }
}

export function signSellerReturnTo(value: string): string {
  const path = normalizeSellerReturnTo(value);
  return `${Buffer.from(path).toString("base64url")}.${signValue(path, sessionSigningKey())}`;
}

export function readSellerReturnTo(token: string | null): string | null {
  if (!token) return null;
  const [encoded, signature, extra] = token.split(".");
  if (!encoded || !signature || extra) return null;
  let path: string;
  try { path = Buffer.from(encoded, "base64url").toString("utf8"); } catch { return null; }
  return constantTimeEquals(signature, signValue(path, sessionSigningKey())) ? normalizeSellerReturnTo(path) : null;
}

export function createSignedSellerScope(actor: DevelopmentSellerActor, audience?: string): string {
  if (!actor.membership) throw new BffError({ code: "WEB_SELLER_CONTEXT_REQUIRED", message: "판매자 범위를 확인할 수 없습니다.", status: 403 });
  const now = Math.floor(Date.now() / 1000);
  const claims: SellerScopeClaims = {
    aud: audience ?? getConfig().sellerScopeAudience ?? "development-seller-management",
    exp: now + 60,
    iat: now,
    jti: randomUUID(),
    permissionVersion: actor.membership.permissionVersion,
    sellerId: actor.membership.sellerId,
    sellerMembershipId: actor.membership.id,
    sellerMembershipVersion: actor.membership.version,
    sessionId: actor.sessionId,
    sub: actor.userId,
  };
  return signJson(claims, sellerScopeKey());
}

export function verifySignedSellerScope(token: string, audience: string): SellerScopeClaims | null {
  const value = verifySignedJson(token, sellerScopeKey());
  if (!isSellerScopeClaims(value)) return null;
  const now = Math.floor(Date.now() / 1000);
  return value.aud === audience && value.exp > now && value.iat <= now ? value : null;
}

export function readTrustedAuditClientIp(headers: Pick<Headers, "get">): string | null {
  const config = getConfig();
  if (config.developmentMocks) return "127.0.0.1";
  const proof = headers.get("x-dropmong-ingress-proof");
  const ip = headers.get("x-dropmong-client-ip")?.trim() ?? "";
  if (!config.trustedIngressSecret || !proof || !constantTimeEquals(proof, signValue(ip, config.trustedIngressSecret)) || isIP(ip) === 0) {
    return null;
  }
  return ip;
}

function normalizeSellerReturnTo(value: string): string {
  if (!value.startsWith("/seller") || value.startsWith("//") || value.includes("\\")) return "/seller";
  try {
    const parsed = new URL(value, "http://local");
    return parsed.origin === "http://local" && parsed.pathname.startsWith("/seller") ? `${parsed.pathname}${parsed.search}` : "/seller";
  } catch { return "/seller"; }
}

function sessionSigningKey(): string { return getConfig().sessionCookieSecret; }
function sellerScopeKey(): string { return getConfig().sellerScopeSigningKey ?? sessionSigningKey(); }
function signValue(value: string, key: string): string { return createHmac("sha256", key).update(value).digest("base64url"); }
function signJson(value: unknown, key: string): string {
  const payload = Buffer.from(JSON.stringify(value)).toString("base64url");
  return `${payload}.${signValue(payload, key)}`;
}
function verifySignedJson(token: string, key: string): unknown {
  const [payload, signature, extra] = token.split(".");
  if (!payload || !signature || extra || !constantTimeEquals(signature, signValue(payload, key))) return null;
  try { return JSON.parse(Buffer.from(payload, "base64url").toString("utf8")); } catch { return null; }
}
function constantTimeEquals(left: string, right: string): boolean {
  const a = Buffer.from(left); const b = Buffer.from(right);
  return a.length === b.length && timingSafeEqual(a, b);
}
function isDevelopmentSellerActor(value: unknown): value is DevelopmentSellerActor {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const candidate = value as Partial<DevelopmentSellerActor>;
  return typeof candidate.userId === "string" && typeof candidate.sessionId === "string" && typeof candidate.csrfToken === "string"
    && typeof candidate.expiresAt === "number" && typeof candidate.onboardingAllowed === "boolean"
    && Array.isArray(candidate.recentAuthPurposes) && (candidate.membership === null || isSellerMembership(candidate.membership));
}
function isSellerMembership(value: unknown): boolean {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const item = value as Record<string, unknown>;
  return typeof item.id === "string" && typeof item.sellerId === "string" && item.status === "ACTIVE" && Array.isArray(item.permissions);
}
function isSellerScopeClaims(value: unknown): value is SellerScopeClaims {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const item = value as Record<string, unknown>;
  return ["aud", "jti", "permissionVersion", "sellerId", "sellerMembershipId", "sellerMembershipVersion", "sessionId", "sub"].every((key) => typeof item[key] === "string")
    && typeof item.exp === "number" && typeof item.iat === "number";
}
