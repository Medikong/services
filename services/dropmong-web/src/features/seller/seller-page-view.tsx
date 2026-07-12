import { SellerAnalyticsView } from "@/features/seller/analytics/analytics-view";
import { SellerCouponsView } from "@/features/seller/coupons/coupons-view";
import { SellerDashboardView } from "@/features/seller/dashboard/dashboard-view";
import { SellerDropEditorView } from "@/features/seller/drop-editor/drop-editor-view";
import { SellerDropsView } from "@/features/seller/drops/drops-view";
import { SellerIssuesView } from "@/features/seller/issues/issues-view";
import { SellerMembersView } from "@/features/seller/members/members-view";
import { SellerOrdersView } from "@/features/seller/orders/orders-view";
import { SellerProductsView } from "@/features/seller/products/products-view";
import { SellerReviewView } from "@/features/seller/review/review-view";
import { SellerSettlementsView } from "@/features/seller/settlements/settlements-view";
import { SellerStoreView } from "@/features/seller/store/store-view";
import type { SellerViewProps } from "@/components/seller/seller-page-primitives";
import type { SellerPageKind } from "@/server/bff/seller/contracts/types";

const views: Record<SellerPageKind, React.ComponentType<SellerViewProps>> = {
  dashboard: SellerDashboardView,
  drops: SellerDropsView,
  products: SellerProductsView,
  "drop-editor": SellerDropEditorView,
  review: SellerReviewView,
  orders: SellerOrdersView,
  coupons: SellerCouponsView,
  analytics: SellerAnalyticsView,
  settlements: SellerSettlementsView,
  store: SellerStoreView,
  members: SellerMembersView,
  issues: SellerIssuesView,
};

export function SellerPageView({ kind, ...props }: SellerViewProps & { kind: SellerPageKind }) {
  const View = views[kind];
  return <View {...props} />;
}
