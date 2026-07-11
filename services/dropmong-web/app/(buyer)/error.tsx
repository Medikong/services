"use client";

export default function BuyerError({ reset }: { error: Error & { digest?: string }; reset: () => void }) {
  return <main className="page-shell page-problem" id="main-content"><span className="eyebrow">TEMPORARY ERROR</span><h1>화면을 준비하지 못했어요.</h1><p>요청을 다시 시도하거나 잠시 후 홈에서 다시 시작해 주세요.</p><button className="button button--primary" onClick={reset} type="button">다시 시도하기</button></main>;
}
