import Link from "next/link";
import { headers } from "next/headers";

import { BuyerHeader } from "@/components/buyer-header";
import { formatDateTime, formatKrw } from "@/components/format";
import { Icon } from "@/components/icons";
import { OrderStatusPanel } from "@/components/order-status";
import { PageProblem } from "@/components/page-problem";
import { getServerActor } from "@/server/bff/auth";
import { getOrderResult } from "@/server/bff/checkout";
import { loadPageData } from "@/server/bff/page-data";
import { createRequestContext } from "@/server/bff/request-context";

type CompletePageProps = {
  searchParams: Promise<{ orderId?: string | string[] }>;
};

export default async function OrderCompletePage({ searchParams }: CompletePageProps) {
  const query = await searchParams;
  const orderId = typeof query.orderId === "string" ? query.orderId : undefined;
  if (!orderId) {
    return <PageProblem detail="결제 완료 후 전달된 주문 식별자가 필요합니다." title="주문 정보를 찾을 수 없어요" />;
  }
  if (!(await getServerActor())) {
    return <PageProblem detail="주문 결과는 로그인한 구매자만 확인할 수 있습니다." title="로그인이 필요해요" />;
  }

  const context = createRequestContext(await headers(), "/orders/complete");
  const result = await loadPageData(() => getOrderResult(context, orderId));
  if (!result.ok) {
    return <PageProblem detail={result.problem.detail} retryHref={`/orders/complete?orderId=${encodeURIComponent(orderId)}`} title="주문 상태를 확인할 수 없어요" />;
  }
  const order = result.value;
  return (
    <main id="main-content">
      <BuyerHeader backHref="/" />
      <div className="page-shell complete-page">
        <section className="complete-hero"><OrderStatusPanel initialStatus={order.status} orderId={order.id} /></section>
        <section className="complete-card"><div className="order-summary-row"><span>주문번호</span><strong>{order.id}</strong></div><div className="order-summary-row"><span>결제일시</span><strong>{formatDateTime(order.confirmedAt ?? order.createdAt)}</strong></div><div className="order-summary-row"><span>최종 결제 금액</span><strong className="price-emphasis">{formatKrw(order.amount)}</strong></div></section>
        <section className="complete-card delivery-progress"><div className="delivery-progress__heading"><Icon name="truck" /><div><span>예상 배송</span><strong>{order.deliveryExpectedAt}</strong></div></div><ol><li className="is-current">주문 완료</li><li>배송 준비</li><li>배송 중</li><li>배송 완료</li></ol></section>
        <section className="complete-card product-confirmation"><h2>구매 상품</h2><div><span className="product-confirmation__thumb" /><p><strong>{order.product.name}</strong><span>옵션 {order.product.optionLabel} · 수량 {order.product.quantity}개</span></p><b>{formatKrw(order.amount)}</b></div></section>
        <section className="development-callout" role="note"><span>개발용 mock</span><p>주문 ID와 배송 일정은 실제 주문 원장 연결 전까지 서버 fixture로 복원됩니다.</p></section>
        <div className="complete-actions"><Link className="button button--primary button--wide" href="/">홈으로 가기</Link></div>
      </div>
    </main>
  );
}
