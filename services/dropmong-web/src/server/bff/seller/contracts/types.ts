export type SellerPermission =
  | "seller.dashboard.read"
  | "seller.drop.read"
  | "seller.drop.write"
  | "seller.drop.review.read"
  | "seller.drop.review.submit"
  | "seller.product.read"
  | "seller.product.write"
  | "seller.order.read"
  | "seller.order.export"
  | "seller.coupon.read"
  | "seller.coupon.write"
  | "seller.analytics.read"
  | "seller.settlement.read"
  | "seller.account.read"
  | "seller.account.write"
  | "seller.store.read"
  | "seller.store.write"
  | "seller.member.read"
  | "seller.member.write"
  | "seller.role.permission.write"
  | "seller.issue.read"
  | "seller.issue.write"
  | "seller.onboarding.start";

export type SellerMembership = {
  id: string;
  sellerId: string;
  version: string;
  permissionVersion: string;
  roleLabel: string;
  status: "ACTIVE" | "INACTIVE";
  permissions: SellerPermission[];
};

export type DevelopmentSellerActor = {
  csrfToken: string;
  expiresAt: number;
  membership: SellerMembership | null;
  onboardingAllowed: boolean;
  recentAuthPurposes: string[];
  sessionId: string;
  userId: string;
};

export type SellerContextDto = {
  actor: { displayName: string; userId: string };
  csrfToken: string;
  membership: null | {
    id: string;
    permissionVersion: string;
    permissions: SellerPermission[];
    roleLabel: string;
    sellerId: string;
    version: string;
  };
  navigation: Array<{ href: string; label: string; permission: SellerPermission }>;
  onboarding: boolean;
  seller: null | { displayName: string; id: string; verificationStatus: string };
};

export type SellerPageKind =
  | "dashboard"
  | "drops"
  | "products"
  | "drop-editor"
  | "review"
  | "orders"
  | "coupons"
  | "analytics"
  | "settlements"
  | "store"
  | "members"
  | "issues";

export type SellerPageData = {
  actions: Array<{ commandPath?: string; href?: string; label: string; method?: "POST"; permission?: SellerPermission; strongAuthPurpose?: string; version?: string }>;
  asOf: string;
  columns: Array<{ key: string; label: string }>;
  description: string;
  emptyMessage: string;
  eyebrow: string;
  filters: Array<{ key: string; label: string; options?: Array<{ label: string; value: string }>; placeholder?: string }>;
  kind: SellerPageKind;
  metrics: Array<{ label: string; note?: string; value: string }>;
  panel: null | { body: string; id: string; title: string };
  partial: boolean;
  readOnly: boolean;
  rows: Array<Record<string, string>>;
  sections?: Record<string, Array<Record<string, string>>>;
  stale: boolean;
  title: string;
  unavailableSections: string[];
  version: string;
};

export type SellerCommandResult = {
  message: string;
  operationId: string;
  status: "accepted" | "completed";
  version: string;
};

export type SellerScopeClaims = {
  aud: string;
  exp: number;
  iat: number;
  jti: string;
  permissionVersion: string;
  sellerId: string;
  sellerMembershipId: string;
  sellerMembershipVersion: string;
  sessionId: string;
  sub: string;
};
