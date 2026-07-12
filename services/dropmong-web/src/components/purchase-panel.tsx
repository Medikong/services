"use client";

import { useMemo, useState } from "react";

import { formatKrw } from "@/components/format";
import { Icon } from "@/components/icons";

type PurchasePanelProps = {
  canStartCheckout: boolean;
  dropId: string;
  productId: string;
  price: number;
  remainingQuantity: number;
};

export function PurchasePanel({
  canStartCheckout,
  dropId,
  productId,
  price,
  remainingQuantity,
}: PurchasePanelProps) {
  const [quantity, setQuantity] = useState(1);
  const [size, setSize] = useState("M");
  const maxQuantity = Math.max(1, Math.min(10, remainingQuantity));
  const checkoutHref = useMemo(() => {
    const rawSeed = JSON.stringify({ dropId, option: size, productId, quantity });
    const seed = bytesToBase64Url(new TextEncoder().encode(rawSeed));
    return `/checkout?checkoutId=${encodeURIComponent(`dev.${seed}`)}`;
  }, [dropId, productId, quantity, size]);

  return (
    <section aria-label="구매 옵션" className="purchase-panel">
      <div className="purchase-panel__option-row">
        <span className="field-label">사이즈</span>
        <div className="size-options" role="group" aria-label="사이즈 선택">
          {["S", "M", "L", "XL"].map((candidate) => (
            <button
              aria-pressed={size === candidate}
              className={`size-option ${size === candidate ? "is-selected" : ""}`}
              key={candidate}
              onClick={() => setSize(candidate)}
              type="button"
            >
              {candidate}
            </button>
          ))}
        </div>
      </div>
      <div className="purchase-panel__option-row purchase-panel__option-row--quantity">
        <span className="field-label">수량</span>
        <div className="quantity-control">
          <button aria-label="수량 줄이기" disabled={quantity === 1} onClick={() => setQuantity((current) => current - 1)} type="button">-</button>
          <output aria-live="polite">{quantity}</output>
          <button aria-label="수량 늘리기" disabled={quantity >= maxQuantity} onClick={() => setQuantity((current) => current + 1)} type="button">+</button>
        </div>
        <span className="stock-note">표시 재고 {remainingQuantity.toLocaleString("ko-KR")}개</span>
      </div>
      <p className="purchase-panel__notice">옵션·수량, 표시 재고와 금액은 개발용 checkout 서버에서 다시 확인합니다.</p>
      <div className="purchase-bar">
        <div>
          <span>상품 금액</span>
          <strong>{formatKrw(price * quantity)}</strong>
        </div>
        {canStartCheckout ? (
          <a className="button button--primary" href={checkoutHref}>
            <Icon name="bag" /> 바로 구매하기
          </a>
        ) : (
          <button className="button button--muted" disabled type="button">현재 구매할 수 없어요</button>
        )}
      </div>
    </section>
  );
}

function bytesToBase64Url(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary).replaceAll("+", "-").replaceAll("/", "_").replaceAll("=", "");
}
