import Link from "next/link";

import { SellerActionForm } from "@/components/seller/seller-action-form";
import { SellerIcon, type SellerIconName } from "@/components/seller/seller-icon";
import type { SellerPageData } from "@/server/bff/seller/contracts/types";

export type SellerViewProps = {
  csrfToken: string;
  page: SellerPageData;
  search: URLSearchParams;
};

export function SellerPageHeader({ page }: { page: SellerPageData }) {
  const primary = page.actions.find((action) => action.href);
  return <header className="seller-page-header"><div><h1>{page.title}</h1><p>{page.description}</p></div>{primary?.href ? <Link className="seller-button seller-button--primary" href={primary.href}><span aria-hidden="true">＋</span>{primary.label}</Link> : null}</header>;
}

export function SellerPageState({ page }: { page: SellerPageData }) {
  return <div className="seller-page-state"><p className="seller-freshness"><span className={page.stale ? "is-stale" : ""}>{page.stale ? "읽기 전용 snapshot" : "집계 완료"}</span><time dateTime={page.asOf}>{formatAsOf(page.asOf)}</time></p>{page.stale ? <div className="seller-alert seller-alert--warning" role="status"><strong>최신 자료를 확인할 수 없습니다.</strong><span>마지막 성공 시각의 사전 마스킹 자료이며 생성·다운로드·편집 작업은 중지됩니다.</span></div> : null}{page.partial ? <div className="seller-alert seller-alert--info" role="status"><strong>일부 자료만 표시합니다.</strong><span>{page.unavailableSections.join(", ")} 구획을 불러오지 못했습니다. 다른 값은 각 기준 시각과 함께 확인할 수 있습니다.</span></div> : null}</div>;
}

export function SellerMetricGrid({ metrics, columns = 4 }: { metrics: SellerPageData["metrics"]; columns?: 3 | 4 | 5 }) {
  const icons: SellerIconName[] = ["package", "orders", "analytics", "settlement", "issue"];
  return <section aria-label="핵심 지표" className={`seller-metrics seller-metrics--${columns}`}>{metrics.map((metric, index) => <article key={metric.label}><span className="seller-metric-icon"><SellerIcon name={icons[index % icons.length]} /></span><div><small>{metric.label}</small><strong>{metric.value}</strong>{metric.note ? <p>{metric.note}</p> : null}</div></article>)}</section>;
}

export function SellerFilterBar({ page, resultCount, search }: { page: SellerPageData; resultCount: number; search: URLSearchParams }) {
  if (page.filters.length === 0) return null;
  return <form className="seller-filter-bar" method="get"><div className="seller-filter-fields">{page.filters.map((filter) => <label key={filter.key}><span>{filter.label}</span>{filter.options ? <select defaultValue={search.get(filter.key) ?? ""} name={filter.key}>{filter.options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</select> : <input defaultValue={search.get(filter.key) ?? ""} name={filter.key} placeholder={filter.placeholder} type={filter.key === "from" || filter.key === "to" ? "date" : "search"} />}</label>)}<button className="seller-button seller-button--secondary" type="submit"><SellerIcon name="search" />조건 적용</button><Link className="seller-button seller-button--text" href="?">초기화</Link></div><p><strong>{resultCount}개</strong> 결과{Array.from(search.keys()).length > 0 ? <span> · URL에 적용된 필터 {Array.from(search.keys()).filter((key) => !key.endsWith("Id") && key !== "panel").length}개</span> : null}</p></form>;
}

export function SellerSection({ actions, children, description, title, className = "" }: { actions?: React.ReactNode; children: React.ReactNode; description?: string; title: string; className?: string }) {
  return <section className={`seller-section ${className}`}><header className="seller-section__header"><div><h2>{title}</h2>{description ? <p>{description}</p> : null}</div>{actions}</header>{children}</section>;
}

export function SellerStatusBadge({ children }: { children: React.ReactNode }) {
  const value = String(children);
  const tone = /반려|오류|지연|부족|실패/.test(value) ? "danger" : /대기|보류|확인|미제출|작성/.test(value) ? "warning" : /완료|승인|판매 가능|활성|해결|정상/.test(value) ? "success" : /진행|배송|검수|접수/.test(value) ? "info" : "neutral";
  return <span className={`seller-badge seller-badge--${tone}`}>{children}</span>;
}

export function SellerProgress({ label, value }: { label: string; value: number }) {
  const safe = Math.max(0, Math.min(100, value));
  return <div className="seller-progress"><div><span>{label}</span><strong>{safe}%</strong></div><div aria-label={`${label} ${safe}%`} aria-valuemax={100} aria-valuemin={0} aria-valuenow={safe} role="progressbar"><i style={{ width: `${safe}%` }} /></div></div>;
}

export function SellerEmptyState({ filtered, message }: { filtered: boolean; message: string }) {
  return <div className="seller-empty"><span className="seller-empty__icon"><SellerIcon name={filtered ? "search" : "package"} /></span><strong>{filtered ? "조건에 맞는 결과가 없습니다" : "아직 표시할 자료가 없습니다"}</strong><p>{message}</p>{filtered ? <Link className="seller-button seller-button--secondary" href="?">필터 초기화</Link> : null}</div>;
}

export function SellerCommandCards({ csrfToken, page, title = "작업" }: { csrfToken: string; page: SellerPageData; title?: string }) {
  const actions = page.actions.filter((action) => action.method === "POST" && action.commandPath);
  if (actions.length === 0) return null;
  return <section className="seller-command-area"><h2 className="sr-only">{title}</h2>{actions.map((action) => <div className="seller-command" key={action.label}><div><strong>{action.label}</strong><span>서버가 현재 권한과 변경 version을 다시 확인합니다.</span></div><SellerActionForm actionPath={action.commandPath!} csrfToken={csrfToken} label={action.label} readOnly={page.readOnly} strongAuthPurpose={action.strongAuthPurpose} version={action.version ?? page.version} /></div>)}</section>;
}

export function SellerThumbnail({ label, tone = 0 }: { label: string; tone?: number }) {
  return <span aria-label={`${label} 이미지`} className={`seller-thumbnail seller-thumbnail--${tone % 4}`} role="img"><SellerIcon name="package" /></span>;
}

export function SellerDataUnavailable({ label }: { label: string }) {
  return <div className="seller-section-unavailable" role="status"><SellerIcon name="issue" /><div><strong>{label} 자료를 표시할 수 없습니다</strong><span>원천 계약에서 이 구획을 제공할 때까지 성공 값으로 대체하지 않습니다.</span></div></div>;
}

export function getSection(page: SellerPageData, key: string): Array<Record<string, string>> { return page.sections?.[key] ?? []; }

export function getCloseHref(path: string, search: URLSearchParams): string {
  const next = new URLSearchParams(search);
  ["productId", "orderId", "couponId", "proposalId", "settlementId", "memberId", "issueId", "panel", "exportId"].forEach((key) => next.delete(key));
  const query = next.toString();
  return `${path}${query ? `?${query}` : ""}`;
}

function formatAsOf(value: string): string { return new Intl.DateTimeFormat("ko-KR", { dateStyle: "medium", timeStyle: "short" }).format(new Date(value)); }
