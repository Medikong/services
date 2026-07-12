import { headers } from "next/headers";
import { notFound, redirect } from "next/navigation";

import { SellerTypedProblem } from "@/components/seller/seller-typed-problem";
import { SellerPageView } from "@/features/seller/seller-page-view";
import { isBffError } from "@/server/bff/errors";
import { createRequestContext } from "@/server/bff/request-context";
import type { SellerPageData, SellerPageKind } from "@/server/bff/seller/contracts/types";
import { getSellerPageFixture } from "@/server/bff/seller/clients/fixtures";
import { getServerSellerActor } from "@/server/bff/seller/context";
import { getSellerPage } from "@/server/bff/seller/modules/pages";

type SearchRecord = Record<string, string | string[] | undefined>;

export async function SellerWorkspacePage({ kind, resourceId, searchParams }: { kind: SellerPageKind; resourceId?: string; searchParams: Promise<SearchRecord> }) {
  const actor = await getServerSellerActor();
  if (!actor) redirect("/auth/signin");
  const search = toUrlSearchParams(await searchParams);
  const context = createRequestContext(await headers(), `/seller/${kind}`);
  let page: SellerPageData;
  if (kind === "store" && actor.onboardingAllowed && !actor.membership) {
    page = { ...getSellerPageFixture("store", search), title: "판매자 등록", description: "판매자 계정과 대표 관리자 membership을 먼저 만듭니다.", rows: [], metrics: [], actions: [] };
  } else {
    try {
      page = await getSellerPage(context, actor, kind, search, resourceId);
    } catch (error) {
      if (!isBffError(error)) throw error;
      if (error.status === 404) notFound();
      return <SellerTypedProblem code={error.code} message={error.message} retryHref={pathForKind(kind)} status={error.status} />;
    }
  }
  const visiblePage = search.get("q") === "empty" ? { ...page, rows: [] } : page;
  return <SellerPageView csrfToken={actor.csrfToken} kind={kind} page={visiblePage} search={search} />;
}

function pathForKind(kind: SellerPageKind): string {
  return {
    dashboard: "/seller", drops: "/seller/drops", products: "/seller/products", "drop-editor": "/seller/drops/new",
    review: "/seller/drops", orders: "/seller/orders", coupons: "/seller/coupons", analytics: "/seller/analytics",
    settlements: "/seller/settlements", store: "/seller/settings/store", members: "/seller/settings/members", issues: "/seller/issues",
  }[kind];
}

function toUrlSearchParams(record: SearchRecord): URLSearchParams {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(record)) {
    if (typeof value === "string") search.set(key, value);
    else value?.forEach((item) => search.append(key, item));
  }
  return search;
}
