import { describe, expect, it } from "vitest";

import { confirmCheckout, getCheckoutSnapshot, getOrderResult } from "@/server/bff/checkout";
import { createDevelopmentActor } from "@/server/bff/security";
import type { RequestContext } from "@/server/bff/request-context";

const context: RequestContext = {
  method: "GET",
  requestId: "test-request-id",
  route: "/test",
  traceparent: "00-00000000000000000000000000000000-0000000000000001-01",
};

const checkoutId = `dev.${Buffer.from(JSON.stringify({ dropId: "drop-001", option: "L", productId: "product-001", quantity: 2 })).toString("base64url")}`;
const soldOutCheckoutId = `dev.${Buffer.from(JSON.stringify({ dropId: "drop-sold-out-001", option: "M", productId: "product-sold-out-001", quantity: 1 })).toString("base64url")}`;

describe("development checkout adapter", () => {
  it("calculates the checkout snapshot on the server from the current catalog product", async () => {
    const checkout = await getCheckoutSnapshot(context, checkoutId);

    expect(checkout.item.quantity).toBe(2);
    expect(checkout.item.optionLabel).toBe("L");
    expect(checkout.totals.subtotal).toBe(178000);
    expect(checkout.totals.total).toBe(178000);
    expect(checkout.source).toBe("development-mock");
  });

  it("returns a deterministic order id and restores its canonical development result", async () => {
    const confirmation = await confirmCheckout(context, checkoutId, createDevelopmentActor());
    const order = await getOrderResult(context, confirmation.orderId);

    expect(confirmation.state).toBe("CONFIRMED");
    expect(order.status).toBe("CONFIRMED");
    expect(order.product.optionLabel).toBe("L");
    expect(order.amount).toBe(178000);
  });

  it("does not confirm a quantity that the development checkout fixture cannot reserve", async () => {
    const checkout = await getCheckoutSnapshot(context, soldOutCheckoutId);

    expect(checkout.actions.canConfirm).toBe(false);
    await expect(confirmCheckout(context, soldOutCheckoutId, createDevelopmentActor())).rejects.toMatchObject({
      code: "WEB_STATE_CONFLICT",
      status: 409,
    });
  });
});
