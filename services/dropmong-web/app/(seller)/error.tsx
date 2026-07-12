"use client";

import Link from "next/link";

export default function SellerParentError({ error, reset }: { error: Error & { digest?: string }; reset: () => void }) {
  return <main className="seller-boundary page-shell" id="main-content"><span>SELLER PORTAL</span><h1>판매자 포털을 열 수 없습니다</h1><p>{error.message || "인증 또는 판매자 범위를 확인하는 중 문제가 발생했습니다."}</p><div><button className="seller-button seller-button--primary" onClick={reset} type="button">다시 확인</button><Link className="seller-button seller-button--secondary" href="/">구매자 홈</Link></div></main>;
}
