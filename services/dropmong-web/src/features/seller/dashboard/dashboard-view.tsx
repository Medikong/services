import Link from "next/link";

import { SellerIcon } from "@/components/seller/seller-icon";
import { SellerDataUnavailable, SellerMetricGrid, SellerPageHeader, SellerPageState, SellerProgress, SellerSection, SellerStatusBadge, SellerThumbnail, getSection, type SellerViewProps } from "@/components/seller/seller-page-primitives";

export function SellerDashboardView({ page }: SellerViewProps) {
  const drop = getSection(page, "ongoingDrops")[0];
  const trend = getSection(page, "salesTrend");
  const orders = getSection(page, "recentOrders");
  const schedule = getSection(page, "schedule");
  return <div className="seller-workspace seller-dashboard"><SellerPageHeader page={page} /><SellerPageState page={page} /><SellerMetricGrid metrics={page.metrics} />
    <div className="seller-dashboard-grid">
      <SellerSection className="seller-priority" title="확인 필요한 작업" description="기한과 영향이 큰 순서입니다." actions={<span className="seller-count">{page.rows.length}</span>}>
        <div className="seller-priority-list">{page.rows.map((row) => <Link href={row.href ?? "/seller"} key={row.task}><span className="seller-priority-icon"><SellerIcon name={/반려|보류/.test(row.state) ? "issue" : "orders"} /></span><div><strong>{row.task}</strong><small>{row.deadline}</small></div><SellerStatusBadge>{row.state}</SellerStatusBadge><SellerIcon name="chevron" /></Link>)}</div>
      </SellerSection>
      <SellerSection className="seller-dashboard-drop" title="진행 중인 드롭" actions={<Link href="/seller/drops">전체 보기</Link>}>
        {drop ? <div className="seller-featured-drop"><SellerThumbnail label={drop.name} /><div><span><SellerStatusBadge>{drop.status}</SellerStatusBadge><strong className="seller-countdown">D-00 {drop.countdown}</strong></span><h3>{drop.name}</h3><SellerProgress label="재고 소진율" value={Number(drop.inventory)} /><p>판매 수량 <strong>{drop.sold}</strong></p><div><Link className="seller-button seller-button--secondary" href="/seller/drops">드롭 관리</Link><Link className="seller-button seller-button--primary" href="/seller/analytics">판매 현황</Link></div></div></div> : <SellerDataUnavailable label="진행 드롭" />}
      </SellerSection>
      <SellerSection className="seller-dashboard-chart" title="판매 현황" description="주문 수와 매출액을 함께 확인합니다." actions={<Link href="/seller/analytics">상세 분석</Link>}>
        {trend.length ? <><div aria-hidden="true" className="seller-line-chart">{trend.map((item, index) => <span key={item.label} style={{ height: `${28 + Number(item.orders) / 2}%` }}><i /><small>{index + 1}</small></span>)}</div><details className="seller-chart-table"><summary>차트 데이터 표로 보기</summary><table><caption>최근 판매 현황</caption><thead><tr><th scope="col">날짜</th><th scope="col">주문</th><th scope="col">매출액</th></tr></thead><tbody>{trend.map((item) => <tr key={item.label}><th scope="row">{item.label}</th><td>{item.orders}건</td><td>{Number(item.sales).toLocaleString("ko-KR")}원</td></tr>)}</tbody></table></details></> : <SellerDataUnavailable label="판매 현황" />}
      </SellerSection>
      <SellerSection className="seller-dashboard-orders" title="최근 주문" actions={<Link href="/seller/orders">전체 보기</Link>}>{orders.length ? <div className="seller-table-scroll"><table><caption>최근 주문 3건</caption><thead><tr><th scope="col">주문번호</th><th scope="col">상품</th><th scope="col">상태</th><th scope="col">결제 금액</th></tr></thead><tbody>{orders.map((order) => <tr key={order.order}><th scope="row">{order.order}</th><td>{order.item}</td><td><SellerStatusBadge>{order.status}</SellerStatusBadge></td><td className="seller-number">{order.amount}</td></tr>)}</tbody></table></div> : <SellerDataUnavailable label="최근 주문" />}</SellerSection>
      <SellerSection className="seller-dashboard-schedule" title="예정된 일정">{schedule.length ? <ol className="seller-schedule">{schedule.map((item) => <li key={`${item.date}-${item.title}`}><span><SellerIcon name="calendar" /></span><div><strong>{item.title}</strong><small>{item.date} · {item.type}</small></div></li>)}</ol> : <SellerDataUnavailable label="예정 일정" />}</SellerSection>
    </div>
  </div>;
}
