import "server-only";

import { cookies } from "next/headers";

import { getConfig } from "@/server/bff/config";
import { BffError } from "@/server/bff/errors";
import type { DevelopmentSellerActor, SellerContextDto } from "@/server/bff/seller/contracts/types";
import { developmentSellerSessionCookieName, readDevelopmentSellerActor } from "@/server/bff/seller/security";

const navigation: SellerContextDto["navigation"] = [
  { href: "/seller", label: "대시보드", permission: "seller.dashboard.read" },
  { href: "/seller/drops", label: "드롭", permission: "seller.drop.read" },
  { href: "/seller/products", label: "상품", permission: "seller.product.read" },
  { href: "/seller/orders", label: "주문", permission: "seller.order.read" },
  { href: "/seller/coupons", label: "쿠폰·제휴", permission: "seller.coupon.read" },
  { href: "/seller/analytics", label: "분석", permission: "seller.analytics.read" },
  { href: "/seller/settlements", label: "정산", permission: "seller.settlement.read" },
  { href: "/seller/issues", label: "운영 이슈", permission: "seller.issue.read" },
  { href: "/seller/settings/store", label: "스토어 설정", permission: "seller.store.read" },
  { href: "/seller/settings/members", label: "팀·권한", permission: "seller.member.read" },
];

export function assertSellerPortalEnabled(): void {
  if (!getConfig().sellerPortalEnabled) {
    throw new BffError({ code: "WEB_RESOURCE_NOT_FOUND", message: "요청한 페이지를 찾을 수 없습니다.", status: 404 });
  }
}

export async function getServerSellerActor(): Promise<DevelopmentSellerActor | null> {
  assertSellerPortalEnabled();
  if (!getConfig().developmentMocks) {
    throw new BffError({ code: "WEB_AUTH_CONTRACT_UNAVAILABLE", message: "판매자 인증 계약이 연결되지 않았습니다.", status: 503 });
  }
  const store = await cookies();
  return readDevelopmentSellerActor(store.get(developmentSellerSessionCookieName)?.value);
}

export function getRequestSellerActor(request: Request): DevelopmentSellerActor {
  assertSellerPortalEnabled();
  if (!getConfig().developmentMocks) {
    throw new BffError({ code: "WEB_AUTH_CONTRACT_UNAVAILABLE", message: "판매자 인증 계약이 연결되지 않았습니다.", status: 503 });
  }
  const actor = readDevelopmentSellerActor(readCookie(request.headers.get("cookie"), developmentSellerSessionCookieName));
  if (!actor) throw new BffError({ code: "WEB_AUTH_REQUIRED", message: "로그인이 필요합니다.", status: 401 });
  return actor;
}

export function toSellerContext(actor: DevelopmentSellerActor): SellerContextDto {
  if (!actor.membership) {
    if (!actor.onboardingAllowed) throw new BffError({ code: "WEB_SELLER_CONTEXT_REQUIRED", message: "판매자 권한이 없습니다.", status: 403 });
    return {
      actor: { displayName: "신규 판매자", userId: actor.userId }, csrfToken: actor.csrfToken,
      membership: null, navigation: [], onboarding: true, seller: null,
    };
  }
  if (actor.membership.status !== "ACTIVE") throw new BffError({ code: "WEB_PERMISSION_DENIED", message: "비활성화된 판매자 멤버십입니다.", status: 403 });
  return {
    actor: { displayName: "김드롭", userId: actor.userId }, csrfToken: actor.csrfToken,
    membership: {
      id: actor.membership.id, permissionVersion: actor.membership.permissionVersion,
      permissions: actor.membership.permissions, roleLabel: actor.membership.roleLabel,
      sellerId: actor.membership.sellerId, version: actor.membership.version,
    },
    navigation: navigation.filter((item) => actor.membership?.permissions.includes(item.permission)),
    onboarding: false,
    seller: { displayName: "드롭몽 스튜디오", id: actor.membership.sellerId, verificationStatus: "검증 완료" },
  };
}

function readCookie(header: string | null, name: string): string | undefined {
  if (!header) return undefined;
  for (const part of header.split(";")) {
    const [key, ...value] = part.trim().split("=");
    if (key === name) return value.join("=");
  }
  return undefined;
}
