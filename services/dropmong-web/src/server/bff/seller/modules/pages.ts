import "server-only";

import type { RequestContext } from "@/server/bff/request-context";
import type { DevelopmentSellerActor, SellerPageData, SellerPageKind, SellerPermission } from "@/server/bff/seller/contracts/types";
import { readSellerPage } from "@/server/bff/seller/clients/seller-management";
import { assertSellerPermission, assertSellerResourceVisible } from "@/server/bff/seller/security";

const permissionByKind: Record<SellerPageKind, SellerPermission> = {
  dashboard: "seller.dashboard.read", drops: "seller.drop.read", products: "seller.product.read",
  "drop-editor": "seller.drop.write", review: "seller.drop.review.read", orders: "seller.order.read",
  coupons: "seller.coupon.read", analytics: "seller.analytics.read", settlements: "seller.settlement.read",
  store: "seller.store.read", members: "seller.member.read", issues: "seller.issue.read",
};

export async function getSellerPage(
  context: RequestContext,
  actor: DevelopmentSellerActor,
  kind: SellerPageKind,
  search: URLSearchParams,
  resourceId?: string,
): Promise<SellerPageData> {
  assertSellerPermission(actor, permissionByKind[kind]);
  if (resourceId) {
    const owner = resourceId.startsWith("foreign-") || resourceId.startsWith("missing-") ? null : actor.membership?.sellerId ?? null;
    assertSellerResourceVisible(owner, actor);
  }
  return readSellerPage(context, actor, kind, search);
}
