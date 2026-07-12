"use client";

import { useRef, useState } from "react";
import { useRouter } from "next/navigation";

import { formatKrw } from "@/components/format";
import { Icon } from "@/components/icons";
import type { CheckoutSnapshot } from "@/server/bff/types";

type CheckoutFormProps = {
  checkout: CheckoutSnapshot;
  csrfToken: string;
};

export function CheckoutForm({ checkout, csrfToken }: CheckoutFormProps) {
  const router = useRouter();
  const idempotencyKey = useRef<string | null>(null);
  const [agreed, setAgreed] = useState(false);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submitPayment() {
    if (!agreed || isSubmitting) {
      return;
    }
    setIsSubmitting(true);
    setError(null);
    idempotencyKey.current ??= crypto.randomUUID();

    try {
      const response = await fetch(`/api/web/checkouts/${encodeURIComponent(checkout.checkoutId)}/confirm`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "Idempotency-Key": idempotencyKey.current,
          "X-CSRF-Token": csrfToken,
        },
        body: JSON.stringify({ agreementConfirmed: true }),
      });
      const body: unknown = await response.json();
      if (!response.ok) {
        setError(readProblemMessage(body));
        return;
      }
      if (!isConfirmResult(body)) {
        setError("결제 결과를 확인할 수 없습니다. 새로고침한 뒤 주문 상태를 확인해 주세요.");
        return;
      }
      router.push(`/orders/complete?orderId=${encodeURIComponent(body.orderId)}`);
    } catch {
      setError("네트워크 문제로 결제 결과를 확인할 수 없습니다. 같은 결제 요청으로 다시 확인해 주세요.");
    } finally {
      setIsSubmitting(false);
    }
  }

  return (
    <section className="checkout-form">
      <div className="development-callout" role="note">
        <span>개발용 mock</span>
        <p>인증, 배송지, 결제수단과 checkout 원장 계약이 준비되기 전까지 옵션·수량을 포함한 서버 fixture로만 동작합니다.</p>
      </div>
      <section className="checkout-card">
        <h2>배송지</h2>
        <div className="detail-line detail-line--icon">
          <Icon name="truck" />
          <div>
            <strong>{checkout.delivery.recipient}</strong>
            <p>{checkout.delivery.phone}</p>
            <p>{checkout.delivery.address}</p>
          </div>
        </div>
      </section>
      <section className="checkout-card">
        <h2>결제 수단</h2>
        <label className="payment-method">
          <input aria-label={`${checkout.paymentMethod.label} 선택됨`} checked readOnly type="radio" />
          <Icon name="credit-card" />
          <span><strong>{checkout.paymentMethod.label}</strong><small>{checkout.paymentMethod.description}</small></span>
        </label>
      </section>
      <section className="checkout-card">
        <h2>혜택</h2>
        <p className="unavailable-copy">{checkout.benefits.coupon}</p>
        <p className="unavailable-copy">{checkout.benefits.point}</p>
      </section>
      <section className="checkout-card">
        <h2>주문 동의</h2>
        <label className="agreement-row">
          <input checked={agreed} onChange={(event) => setAgreed(event.target.checked)} type="checkbox" />
          <span><strong>주문 내용과 개인정보 제공에 동의합니다.</strong><small>필수</small></span>
        </label>
      </section>
      <section className="checkout-card checkout-card--total">
        <h2>결제 금액</h2>
        <dl className="price-list">
          <div><dt>상품 금액</dt><dd>{formatKrw(checkout.totals.subtotal)}</dd></div>
          <div><dt>배송비</dt><dd>{checkout.totals.shippingFee === 0 ? "무료" : formatKrw(checkout.totals.shippingFee)}</dd></div>
          <div><dt>할인</dt><dd>{checkout.totals.discount === 0 ? "-" : `-${formatKrw(checkout.totals.discount)}`}</dd></div>
          <div className="price-list__total"><dt>최종 결제 금액</dt><dd>{formatKrw(checkout.totals.total)}</dd></div>
        </dl>
      </section>
      <p aria-live="polite" className="form-error">{error}</p>
      <button
        className="button button--primary button--wide"
        disabled={!agreed || !checkout.actions.canConfirm || isSubmitting}
        onClick={submitPayment}
        type="button"
      >
        <Icon name="shield" /> {isSubmitting ? "결제 상태를 확인하는 중" : `${formatKrw(checkout.totals.total)} 결제하기`}
      </button>
    </section>
  );
}

function isConfirmResult(value: unknown): value is { orderId: string; state: "CONFIRMED" } {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return false;
  }
  const candidate = value as Record<string, unknown>;
  return typeof candidate.orderId === "string" && candidate.state === "CONFIRMED";
}

function readProblemMessage(value: unknown): string {
  if (value && typeof value === "object" && !Array.isArray(value)) {
    const candidate = value as Record<string, unknown>;
    if (typeof candidate.title === "string") {
      return candidate.title;
    }
  }
  return "결제를 진행할 수 없습니다. 잠시 후 다시 시도해 주세요.";
}
