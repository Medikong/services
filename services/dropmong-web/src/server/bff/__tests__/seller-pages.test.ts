import { describe, expect, it } from "vitest";

import { getSellerPageFixture } from "@/server/bff/seller/clients/fixtures";

describe("seller page contracts", () => {
  it("provides all twelve PAGE families with distinct screen data", () => {
    const kinds = ["dashboard", "drops", "products", "drop-editor", "review", "orders", "coupons", "analytics", "settlements", "store", "members", "issues"] as const;
    const pages = kinds.map((kind) => getSellerPageFixture(kind, new URLSearchParams()));

    expect(new Set(pages.map((page) => page.title))).toHaveLength(12);
    expect(pages.every((page) => page.columns.length > 0)).toBe(true);
  });

  it("keeps degraded order snapshots masked and read-only", () => {
    const page = getSellerPageFixture("orders", new URLSearchParams("state=stale&orderId=order-001"));

    expect(page.stale).toBe(true);
    expect(page.readOnly).toBe(true);
    expect(page.rows[0]?.buyer).toMatch(/\*/);
    expect(page.panel?.id).toBe("order-001");
  });

  it("does not turn a partial analytics failure into empty successful data", () => {
    const page = getSellerPageFixture("analytics", new URLSearchParams("state=partial"));

    expect(page.partial).toBe(true);
    expect(page.unavailableSections).toEqual(["실시간 집계"]);
    expect(page.rows.length).toBeGreaterThan(0);
  });

  it("keeps SellerAccount, StoreProfile and role permissions on independent versions", () => {
    const store = getSellerPageFixture("store", new URLSearchParams());
    const members = getSellerPageFixture("members", new URLSearchParams());

    expect(store.actions.map((action) => action.version)).toEqual(["account-v8", "store-v12"]);
    expect(members.actions.map((action) => action.version)).toEqual(["membership-v4", "permissions-v7"]);
  });
});
