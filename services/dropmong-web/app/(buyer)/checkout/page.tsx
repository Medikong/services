import { headers } from "next/headers";

import { BuyerHeader } from "@/components/buyer-header";
import { CheckoutForm } from "@/components/checkout-form";
import { formatKrw } from "@/components/format";
import { PageProblem } from "@/components/page-problem";
import { getServerActor } from "@/server/bff/auth";
import { getCheckoutSnapshot } from "@/server/bff/checkout";
import { getConfig } from "@/server/bff/config";
import { loadPageData } from "@/server/bff/page-data";
import { createRequestContext } from "@/server/bff/request-context";

type CheckoutPageProps = {
  searchParams: Promise<{ checkoutId?: string | string[] }>;
};

export default async function CheckoutPage({ searchParams }: CheckoutPageProps) {
  const query = await searchParams;
  const checkoutId = typeof query.checkoutId === "string" ? query.checkoutId : undefined;
  if (!checkoutId) {
    return <PageProblem detail="상품 상세에서 구매를 시작해 주세요." title="checkout 정보를 찾을 수 없어요" />;
  }

  const returnTo = `/checkout?checkoutId=${encodeURIComponent(checkoutId)}`;
  const actorResult = await loadPageData(getServerActor);
  if (!actorResult.ok) {
    return <PageProblem detail={actorResult.problem.detail} retryHref={returnTo} title="인증 상태를 확인할 수 없어요" />;
  }
  const actor = actorResult.value;
  if (!actor) {
    return <LoginGate returnTo={returnTo} />;
  }

  const context = createRequestContext(await headers(), "/checkout");
  const result = await loadPageData(() => getCheckoutSnapshot(context, checkoutId));
  if (!result.ok) {
    return <PageProblem detail={result.problem.detail} retryHref={returnTo} title="주문 정보를 불러올 수 없어요" />;
  }
  const checkout = result.value;
  return (
    <main id="main-content">
      <BuyerHeader backHref="/" title="주문 / 결제" />
      <div className="page-shell checkout-page">
        <section className="checkout-item-card"><ProductSummary name={checkout.item.name} optionLabel={checkout.item.optionLabel} quantity={checkout.item.quantity} total={checkout.totals.total} /></section>
        <CheckoutForm checkout={checkout} csrfToken={actor.csrfToken} />
      </div>
    </main>
  );
}

function LoginGate({ returnTo }: { returnTo: string }) {
  const config = getConfig();
  return (
    <main className="page-shell page-problem" id="main-content">
      <span className="eyebrow">LOGIN REQUIRED</span>
      <h1>주문을 계속하려면 로그인이 필요해요.</h1>
      <p>개발 환경에서는 서명된 HttpOnly 구매자 세션으로 주문 화면을 검증할 수 있습니다.</p>
      {config.developmentMocks ? <a className="button button--primary" href={`/api/web/auth/development-session?returnTo=${encodeURIComponent(returnTo)}`}>개발용 구매자 세션 시작</a> : null}
    </main>
  );
}

function ProductSummary({ name, optionLabel, quantity, total }: { name: string; optionLabel: string; quantity: number; total: number }) {
  return <div className="checkout-item-summary"><div className="checkout-item-summary__thumbnail" /><div><span>옵션 {optionLabel} · 상품 {quantity}개</span><strong>{name}</strong></div><b>{formatKrw(total)}</b></div>;
}
