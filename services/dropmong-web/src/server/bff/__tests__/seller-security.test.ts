import { describe, expect, it } from "vitest";

import { getConfig } from "@/server/bff/config";
import { isBffError, RecentAuthRequiredError } from "@/server/bff/errors";
import {
  assertRecentSellerAuth,
  assertSellerMutation,
  assertSellerPermission,
  assertSellerResourceVisible,
  createDevelopmentSellerActor,
  createSignedSellerScope,
  readDevelopmentSellerActor,
  readTrustedAuditClientIp,
  readSellerReturnTo,
  rotateDevelopmentSellerAuth,
  signDevelopmentSellerSession,
  signSellerReturnTo,
  verifySignedSellerScope,
} from "@/server/bff/seller/security";
import { assertSellerPortalEnabled } from "@/server/bff/seller/context";
import { mapSellerDownstreamStatus } from "@/server/bff/seller/clients/seller-management";

describe("seller security boundary", () => {
  it("accepts only an intact signed seller session and signed return path", () => {
    const actor = createDevelopmentSellerActor();
    const session = signDevelopmentSellerSession(actor);
    const returnToken = signSellerReturnTo("/seller/orders?state=stale");

    expect(readDevelopmentSellerActor(session)?.membership?.sellerId).toBe("seller-001");
    expect(readDevelopmentSellerActor(`${session}x`)).toBeNull();
    expect(readSellerReturnTo(returnToken)).toBe("/seller/orders?state=stale");
    expect(readSellerReturnTo(`${returnToken}x`)).toBeNull();
    expect(readSellerReturnTo(signSellerReturnTo("https://evil.example/seller"))).toBe("/seller");
  });

  it("binds the signed seller scope to its audience and detects tampering", () => {
    const token = createSignedSellerScope(createDevelopmentSellerActor(), "seller-management");

    expect(verifySignedSellerScope(token, "seller-management")?.sellerId).toBe("seller-001");
    expect(verifySignedSellerScope(token, "catalog")).toBeNull();
    expect(verifySignedSellerScope(`${token}x`, "seller-management")).toBeNull();
  });

  it("uses permission actions and returns the same 404 for missing and foreign resources", () => {
    const restricted = createDevelopmentSellerActor("restricted");
    expect(() => assertSellerPermission(restricted, "seller.product.read")).not.toThrow();
    expect(captureBffError(() => assertSellerPermission(restricted, "seller.product.write"))).toMatchObject({ status: 403 });

    for (const owner of [null, "seller-other"] as const) {
      expect(captureBffError(() => assertSellerResourceVisible(owner, restricted))).toMatchObject({
        code: "WEB_RESOURCE_NOT_FOUND", status: 404,
      });
    }
  });

  it("rejects an old CSRF token after recent authentication rotates the session", () => {
    const actor = createDevelopmentSellerActor();
    const request = mutationRequest(actor.csrfToken);
    expect(assertSellerMutation(request, actor)).toBe("seller-test-key");

    const rotated = rotateDevelopmentSellerAuth(actor, "seller_order_export");
    expect(captureBffError(() => assertSellerMutation(request, rotated))).toMatchObject({ code: "WEB_CSRF_INVALID" });
  });

  it("returns a typed recent-auth error until the exact purpose is present", () => {
    const actor = createDevelopmentSellerActor();
    expect(() => assertRecentSellerAuth(actor, "seller_member_manage", "/seller/settings/members")).toThrow(RecentAuthRequiredError);
    expect(() => assertRecentSellerAuth(rotateDevelopmentSellerAuth(actor, "seller_member_manage"), "seller_member_manage", "/seller/settings/members")).not.toThrow();
  });

  it("ignores browser forwarding headers when creating the audit IP context", () => {
    const headers = new Headers({ "x-forwarded-for": "203.0.113.8", forwarded: "for=203.0.113.9" });
    expect(readTrustedAuditClientIp(headers)).toBe("127.0.0.1");
  });

  it("maps an upstream version conflict to the typed web conflict", () => {
    expect(mapSellerDownstreamStatus(409)).toMatchObject({ code: "WEB_STATE_CONFLICT", status: 409 });
  });
});

describe("seller production configuration", () => {
  it("fails closed when the portal is enabled without required external contracts", () => {
    expect(() => getConfig({
      APP_ORIGIN: "https://dropmong.example",
      DEV_MOCK_MODE: "false",
      SELLER_PORTAL_ENABLED: "true",
      SESSION_COOKIE_SECRET: "a-secure-session-secret-that-is-long-enough",
    })).toThrow(/SELLER_CONTEXT_INTERNAL_BASE_URL/);
  });

  it("does not expose seller routes when the feature is disabled", () => {
    const previous = process.env.SELLER_PORTAL_ENABLED;
    process.env.SELLER_PORTAL_ENABLED = "false";
    try {
      expect(captureBffError(assertSellerPortalEnabled)).toMatchObject({ code: "WEB_RESOURCE_NOT_FOUND", status: 404 });
    } finally {
      process.env.SELLER_PORTAL_ENABLED = previous;
    }
  });
});

function mutationRequest(csrfToken: string): Request {
  return new Request("http://localhost:3000/api/web/seller/products/save", {
    method: "POST",
    headers: { origin: "http://localhost:3000", "x-csrf-token": csrfToken, "idempotency-key": "seller-test-key" },
  });
}

function captureBffError(action: () => unknown) {
  try {
    action();
  } catch (error) {
    if (isBffError(error)) return error;
    throw error;
  }
  throw new Error("Expected a BffError to be thrown.");
}
