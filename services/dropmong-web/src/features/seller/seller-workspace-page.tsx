import Link from "next/link";
import { headers } from "next/headers";
import { redirect } from "next/navigation";

import { SellerActionForm, SellerOnboardingForm } from "@/components/seller/seller-action-form";
import { createRequestContext } from "@/server/bff/request-context";
import type { SellerPageData, SellerPageKind } from "@/server/bff/seller/contracts/types";
import { getServerSellerActor } from "@/server/bff/seller/context";
import { getSellerPageFixture } from "@/server/bff/seller/clients/fixtures";
import { getSellerPage } from "@/server/bff/seller/modules/pages";

type SearchRecord = Record<string, string | string[] | undefined>;
const panelParamByKind: Partial<Record<SellerPageKind, string>> = {
  products: "productId", orders: "orderId", coupons: "couponId", settlements: "settlementId", members: "memberId", issues: "issueId",
};

export async function SellerWorkspacePage({ kind, resourceId, searchParams }: { kind: SellerPageKind; resourceId?: string; searchParams: Promise<SearchRecord> }) {
  const actor = await getServerSellerActor();
  if (!actor) redirect("/auth/signin");
  const raw = await searchParams;
  const search = toUrlSearchParams(raw);
  const context = createRequestContext(await headers(), `/seller/${kind}`);
  let page: SellerPageData;
  if (kind === "store" && actor.onboardingAllowed && !actor.membership) {
    page = { ...getSellerPageFixture("store", search), title: "판매자 등록", description: "판매자 계정과 대표 관리자 membership을 먼저 만듭니다.", rows: [], metrics: [], actions: [] };
  } else {
    page = await getSellerPage(context, actor, kind, search, resourceId);
  }
  const rows = search.get("q") === "empty" ? [] : page.rows;
  const closePanelHref = createPanelCloseHref(kind, search);
  return (
    <div className="seller-workspace">
      <header className="seller-page-heading">
        <div><span>{page.eyebrow}</span><h1>{page.title}</h1><p>{page.description}</p></div>
        {page.actions.filter((action) => action.href).map((action) => <Link className="seller-button seller-button--primary" href={action.href!} key={action.label}>{action.label}</Link>)}
      </header>
      <p className="seller-freshness"><span className={page.stale ? "is-stale" : ""}>{page.stale ? "제한 상태 · 읽기 전용" : "최신 조회"}</span><time>{formatAsOf(page.asOf)}</time></p>
      {page.stale ? <div className="seller-alert seller-alert--warning" role="status">원천이 허용한 사전 마스킹 snapshot입니다. 생성·다운로드·편집 작업을 사용할 수 없습니다.</div> : null}
      {page.partial ? <div className="seller-alert" role="status">일부 자료를 불러오지 못했습니다: {page.unavailableSections.join(", ")}. 다른 조회 결과는 기준 시각과 함께 표시합니다.</div> : null}
      {page.metrics.length > 0 ? <section aria-label="핵심 지표" className="seller-metrics">{page.metrics.map((metric) => <article key={metric.label}><span>{metric.label}</span><strong>{metric.value}</strong>{metric.note ? <small>{metric.note}</small> : null}</article>)}</section> : null}
      {page.filters.length > 0 ? <form className="seller-filters" method="get">{page.filters.map((filter) => <label key={filter.key}>{filter.label}{filter.options ? <select defaultValue={search.get(filter.key) ?? ""} name={filter.key}>{filter.options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</select> : <input defaultValue={search.get(filter.key) ?? ""} name={filter.key} placeholder={filter.placeholder} />}</label>)}<button className="seller-button seller-button--secondary" type="submit">조건 적용</button></form> : null}
      {kind === "analytics" ? <AccessibleTrend rows={rows} /> : null}
      <section aria-labelledby="seller-list-heading" className="seller-data-card">
        <div className="seller-card-heading"><div><h2 id="seller-list-heading">{kind === "dashboard" ? "우선 작업" : "조회 결과"}</h2><p>{rows.length}개 항목</p></div><span>version {page.version}</span></div>
        {rows.length > 0 ? <div className="seller-table-scroll"><table><thead><tr>{page.columns.map((column) => <th key={column.key} scope="col">{column.label}</th>)}</tr></thead><tbody>{rows.map((row, index) => <tr key={row.id ?? `${kind}-${index}`}>{page.columns.map((column) => <td key={column.key}>{renderCell(row, column.key, kind, search)}</td>)}</tr>)}</tbody></table></div> : <div className="seller-empty"><strong>표시할 결과가 없습니다</strong><p>{page.emptyMessage}</p></div>}
      </section>
      {actor.onboardingAllowed && !actor.membership && kind === "store" ? <section className="seller-form-card"><h2>판매자 등록 정보</h2><SellerOnboardingForm csrfToken={actor.csrfToken} /></section> : null}
      {page.actions.filter((action) => action.method === "POST").map((action) => {
        return action.commandPath ? <section className="seller-form-card" key={action.label}><div><h2>{action.label}</h2><p>서버가 현재 membership, permission, version과 판매자 범위를 다시 확인합니다.</p></div><SellerActionForm actionPath={action.commandPath} csrfToken={actor.csrfToken} label={action.label} readOnly={page.readOnly} strongAuthPurpose={action.strongAuthPurpose} version={action.version ?? page.version} /></section> : null;
      })}
      {page.panel ? <aside aria-labelledby="seller-panel-title" className="seller-detail-panel"><div><span>DETAIL PANEL</span><h2 id="seller-panel-title">{page.panel.title}</h2><p>{page.panel.body}</p><dl><div><dt>식별자</dt><dd>{page.panel.id}</dd></div><div><dt>판매자 범위</dt><dd>서버 확인 완료</dd></div></dl><Link className="seller-button seller-button--secondary" href={closePanelHref}>패널 닫기</Link></div></aside> : null}
    </div>
  );
}

function AccessibleTrend({ rows }: { rows: Array<Record<string, string>> }) {
  return <section aria-labelledby="trend-heading" className="seller-chart"><div><span>CONFIRMED SALES</span><h2 id="trend-heading">매출 변화</h2></div><div aria-hidden="true" className="seller-chart__bars">{rows.map((row, index) => <i key={row.date} style={{ height: `${36 + index * 22}%` }} />)}</div><p>정확한 값은 이어지는 조회 결과 표에서 확인할 수 있습니다.</p></section>;
}
function renderCell(row: Record<string, string>, key: string, kind: SellerPageKind, search: URLSearchParams): React.ReactNode {
  const value = row[key] ?? "-";
  if (key === "href" && value.startsWith("/seller")) return <Link href={value}>작업 열기</Link>;
  if (key === "name" || key === "order" || key === "period" || key === "issue") {
    const param = panelParamByKind[kind];
    if (param && row.id) { const next = new URLSearchParams(search); next.set(param, row.id); return <Link href={`?${next.toString()}`}>{value}</Link>; }
  }
  return value;
}
function createPanelCloseHref(kind: SellerPageKind, search: URLSearchParams): string {
  const next = new URLSearchParams(search);
  ["productId", "orderId", "couponId", "proposalId", "settlementId", "memberId", "issueId", "panel", "exportId"].forEach((key) => next.delete(key));
  const query = next.toString();
  return `${pathForKind(kind)}${query ? `?${query}` : ""}`;
}
function pathForKind(kind: SellerPageKind): string {
  const map: Record<SellerPageKind, string> = { dashboard: "/seller", drops: "/seller/drops", products: "/seller/products", "drop-editor": "/seller/drops/new", review: "/seller/drops", orders: "/seller/orders", coupons: "/seller/coupons", analytics: "/seller/analytics", settlements: "/seller/settlements", store: "/seller/settings/store", members: "/seller/settings/members", issues: "/seller/issues" };
  return map[kind];
}
function toUrlSearchParams(record: SearchRecord): URLSearchParams {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(record)) { if (typeof value === "string") search.set(key, value); else value?.forEach((item) => search.append(key, item)); }
  return search;
}
function formatAsOf(value: string): string { return new Intl.DateTimeFormat("ko-KR", { dateStyle: "medium", timeStyle: "short" }).format(new Date(value)); }
