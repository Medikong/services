"use client";

export default function SellerPageError({ error, reset }: { error: Error & { digest?: string }; reset: () => void }) {
  return <section className="seller-boundary"><span>REQUEST FAILED</span><h1>판매자 자료를 불러오지 못했습니다</h1><p>{error.message || "잠시 후 다시 시도해 주세요."}</p><div><button className="seller-button seller-button--primary" onClick={reset} type="button">다시 시도</button><a className="seller-button seller-button--secondary" href="/seller">대시보드</a></div></section>;
}
