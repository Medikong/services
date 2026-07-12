import "server-only";

import { BffError } from "@/server/bff/errors";
import type { RequestContext } from "@/server/bff/request-context";
import type { DevelopmentSellerActor, SellerCommandResult, SellerPermission } from "@/server/bff/seller/contracts/types";
import { sendSellerCommand } from "@/server/bff/seller/clients/seller-management";
import { assertRecentSellerAuth, assertSellerMutation, assertSellerPermission, readTrustedAuditClientIp } from "@/server/bff/seller/security";

type CommandPolicy = { permission: SellerPermission; purpose?: string; requiresAuditIp?: boolean };
const policies: Record<string, CommandPolicy> = {
  "products/save": { permission: "seller.product.write" },
  "drop-drafts/save": { permission: "seller.drop.write" },
  "reviews/submit": { permission: "seller.drop.review.submit" },
  "coupons/save": { permission: "seller.coupon.write" },
  "store-profile/save": { permission: "seller.store.write" },
  "account/save": { permission: "seller.account.write", purpose: "seller_account_change" },
  "members/invite": { permission: "seller.member.write", purpose: "seller_member_manage" },
  "roles/permissions/save": { permission: "seller.role.permission.write", purpose: "seller_member_manage" },
  "issues/create": { permission: "seller.issue.write" },
  "order-exports/create": { permission: "seller.order.export", purpose: "seller_order_export", requiresAuditIp: true },
};

export async function executeSellerCommand(
  request: Request,
  context: RequestContext,
  actor: DevelopmentSellerActor,
  path: string,
): Promise<SellerCommandResult> {
  const policy = policies[path];
  if (!policy) throw new BffError({ code: "WEB_RESOURCE_NOT_FOUND", message: "요청한 작업을 찾을 수 없습니다.", status: 404 });
  assertSellerPermission(actor, policy.permission);
  const idempotencyKey = assertSellerMutation(request, actor);
  if (policy.purpose) assertRecentSellerAuth(actor, policy.purpose, commandReturnTo(path));
  const auditClientIp = policy.requiresAuditIp ? readTrustedAuditClientIp(request.headers) : null;
  if (policy.requiresAuditIp && !auditClientIp) {
    throw new BffError({ code: "WEB_AUDIT_CONTEXT_REQUIRED", message: "검증된 접속 정보를 확인할 수 없어 자료를 만들 수 없습니다.", status: 403 });
  }
  let body: unknown;
  try { body = await request.json(); } catch { throw new BffError({ code: "WEB_REQUEST_INVALID", message: "요청 본문이 올바르지 않습니다.", status: 400 }); }
  if (!body || typeof body !== "object" || Array.isArray(body)) throw new BffError({ code: "WEB_REQUEST_INVALID", message: "요청 본문이 올바르지 않습니다.", status: 400 });
  return sendSellerCommand(context, actor, path, body, idempotencyKey, auditClientIp);
}

function commandReturnTo(path: string): string {
  if (path === "members/invite" || path === "roles/permissions/save") return "/seller/settings/members";
  if (path === "account/save") return "/seller/settings/store";
  if (path === "order-exports/create") return "/seller/orders";
  return "/seller";
}
