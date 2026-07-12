import { headers } from "next/headers";
import { notFound } from "next/navigation";

import { BuyerHeader } from "@/components/buyer-header";
import { formatKrw } from "@/components/format";
import { Icon } from "@/components/icons";
import { ProductArt } from "@/components/product-art";
import { PurchasePanel } from "@/components/purchase-panel";
import { isBffError } from "@/server/bff/errors";
import { getProductDetailPage } from "@/server/bff/product";
import { createRequestContext } from "@/server/bff/request-context";

type ProductPageProps = {
  params: Promise<{ productId: string }>;
  searchParams: Promise<{ dropId?: string | string[] }>;
};

export default async function ProductPage({ params, searchParams }: ProductPageProps) {
  const { productId } = await params;
  const query = await searchParams;
  const dropId = typeof query.dropId === "string" ? query.dropId : undefined;
  const context = createRequestContext(await headers(), "/products/[productId]");

  let page;
  try {
    page = await getProductDetailPage(context, productId, dropId);
  } catch (error) {
    if (isBffError(error) && error.status === 404) {
      notFound();
    }
    throw error;
  }

  return (
    <main id="main-content">
      <BuyerHeader backHref="/" />
      <div className="page-shell product-page">
        <section className="product-hero">
          <div className="product-visual"><span className="product-visual__badge">{page.drop.status === "OPEN" ? "LIMITED" : page.drop.status}</span><ProductArt tone="dark" /></div>
          <div className="product-summary">
            <span className="brand-tag">DROP {page.drop.id.toUpperCase()}</span>
            <h1>{page.product.name}</h1>
            <strong>{formatKrw(page.product.price)}</strong>
            <p>{page.drop.description ?? "한정 수량으로 준비한 DropMong 드롭 상품입니다."}</p>
            <div className="availability-row"><span className={`availability-dot ${page.actions.canStartCheckout ? "is-open" : ""}`} /><span>{page.actions.availabilityLabel}</span></div>
          </div>
        </section>
        <section className="detail-grid">
          <article className="detail-card"><h2>드롭 구매 안내</h2><div className="rule-grid"><InfoRule icon="clock" title="동일 시간 오픈" description="오픈 시각은 서버 기준으로 확인합니다." /><InfoRule icon="package" title="한정 수량" description="최종 재고 배정은 주문 서비스가 판단합니다." /><InfoRule icon="shield" title="안전한 주문" description="결제 전 서버 snapshot을 다시 확인합니다." /></div></article>
          <article className="detail-card"><h2>배송 안내</h2><p>배송지와 배송 일정은 checkout 계약이 연결되기 전까지 개발용 fixture로 표시됩니다.</p></article>
          <article className="detail-card detail-card--unavailable"><Icon name="heart" /><div><h2>개인화 기능 연결 준비 중</h2><p>{page.personalization.message}</p></div></article>
        </section>
      </div>
      <PurchasePanel canStartCheckout={page.actions.canStartCheckout} dropId={page.drop.id} price={page.product.price} productId={page.product.id} remainingQuantity={page.product.remainingQuantity} />
    </main>
  );
}

function InfoRule({ description, icon, title }: { description: string; icon: "clock" | "package" | "shield"; title: string }) {
  return <div className="rule"><Icon name={icon} /><strong>{title}</strong><p>{description}</p></div>;
}
