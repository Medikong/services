"use client";

export default function GlobalError({ reset }: { error: Error & { digest?: string }; reset: () => void }) {
  return <html lang="ko"><body><main className="page-problem"><h1>서비스를 다시 준비하고 있어요.</h1><p>잠시 후 다시 시도해 주세요.</p><button onClick={reset} type="button">다시 시도하기</button></main></body></html>;
}
