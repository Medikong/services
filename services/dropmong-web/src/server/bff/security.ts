import "server-only";

import { createHmac, randomBytes, timingSafeEqual } from "node:crypto";

import { getConfig } from "@/server/bff/config";
import { BffError } from "@/server/bff/errors";
import type { DevelopmentActor } from "@/server/bff/types";

export const developmentSessionCookieName = "dropmong_dev_session";

type SessionPayload = {
  csrfToken: string;
  expiresAt: number;
  role: "CUSTOMER";
  userId: string;
};

export function createDevelopmentActor(): DevelopmentActor {
  const payload: SessionPayload = {
    csrfToken: randomBytes(24).toString("base64url"),
    expiresAt: Date.now() + 1000 * 60 * 60 * 2,
    role: "CUSTOMER",
    userId: "development-buyer-001",
  };
  return payload;
}

export function signDevelopmentSession(actor: DevelopmentActor): string {
  const encodedPayload = Buffer.from(JSON.stringify(actor)).toString("base64url");
  const signature = sign(encodedPayload);
  return `${encodedPayload}.${signature}`;
}

export function readDevelopmentActor(cookieValue: string | undefined): DevelopmentActor | null {
  if (!cookieValue || !getConfig().developmentMocks) {
    return null;
  }
  const [encodedPayload, receivedSignature, extra] = cookieValue.split(".");
  if (!encodedPayload || !receivedSignature || extra) {
    return null;
  }
  const expectedSignature = sign(encodedPayload);
  if (!constantTimeEquals(receivedSignature, expectedSignature)) {
    return null;
  }

  let value: unknown;
  try {
    value = JSON.parse(Buffer.from(encodedPayload, "base64url").toString("utf8"));
  } catch {
    return null;
  }
  if (!isSessionPayload(value) || value.expiresAt <= Date.now()) {
    return null;
  }
  return value;
}

export function assertSameOrigin(request: Request): void {
  const origin = request.headers.get("origin");
  const expectedOrigin = getConfig().appOrigin.origin;
  if (!origin || origin !== expectedOrigin) {
    throw new BffError({
      code: "WEB_CSRF_INVALID",
      message: "요청 출처를 확인할 수 없습니다.",
      status: 403,
    });
  }
}

export function assertCsrf(request: Request, actor: DevelopmentActor): void {
  const csrfToken = request.headers.get("x-csrf-token");
  if (!csrfToken || !constantTimeEquals(csrfToken, actor.csrfToken)) {
    throw new BffError({
      code: "WEB_CSRF_INVALID",
      message: "보안 확인이 만료되었습니다. 페이지를 새로고침한 뒤 다시 시도해 주세요.",
      status: 403,
    });
  }
}

export function assertIdempotencyKey(request: Request): string {
  const key = request.headers.get("idempotency-key")?.trim();
  if (!key || key.length > 128) {
    throw new BffError({
      code: "WEB_REQUEST_INVALID",
      message: "결제 요청 식별자가 올바르지 않습니다.",
      status: 400,
    });
  }
  return key;
}

function sign(value: string): string {
  return createHmac("sha256", getConfig().sessionCookieSecret).update(value).digest("base64url");
}

function constantTimeEquals(left: string, right: string): boolean {
  const leftBuffer = Buffer.from(left);
  const rightBuffer = Buffer.from(right);
  return leftBuffer.length === rightBuffer.length && timingSafeEqual(leftBuffer, rightBuffer);
}

function isSessionPayload(value: unknown): value is SessionPayload {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return false;
  }
  const candidate = value as Record<string, unknown>;
  return (
    typeof candidate.userId === "string" &&
    candidate.role === "CUSTOMER" &&
    typeof candidate.csrfToken === "string" &&
    typeof candidate.expiresAt === "number"
  );
}
