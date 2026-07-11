import { describe, expect, it } from "vitest";

import { getHomePage } from "@/server/bff/home";
import { getProductDetailPage } from "@/server/bff/product";
import type { RequestContext } from "@/server/bff/request-context";

const context: RequestContext = {
  method: "GET",
  requestId: "test-request-id",
  route: "/test",
  traceparent: "00-00000000000000000000000000000000-0000000000000001-01",
};

describe("public Catalog BFF", () => {
  it("returns an open featured drop and upcoming drops from the development fixture", async () => {
    const page = await getHomePage(context);

    expect(page.featured?.id).toBe("drop-001");
    expect(page.upcoming).toHaveLength(2);
    expect(page.meta.partial).toBe(false);
  });

  it("reads product detail from a product id and an optional drop hint", async () => {
    const page = await getProductDetailPage(context, "product-001", "drop-001");

    expect(page.product.name).toContain("윈드 브레이커");
    expect(page.drop.status).toBe("OPEN");
    expect(page.actions.canStartCheckout).toBe(true);
  });
});
