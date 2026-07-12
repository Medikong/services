"use client";

import Link from "next/link";

import { SellerIcon } from "@/components/seller/seller-icon";

export function SellerRouteProblem({ error, reset }: { error: Error & { digest?: string }; reset: () => void }) {
  const state = classify(error);
  return <section className={`seller-boundary seller-boundary--${state.kind}`}><span>{state.code}</span><div className="seller-boundary__icon"><SellerIcon name={state.kind === "forbidden" ? "user" : "issue"} /></div><h1>{state.title}</h1><p>{state.description}</p><div><button className="seller-button seller-button--primary" onClick={reset} type="button">다시 확인</button><Link className="seller-button seller-button--secondary" href="/seller">판매자 대시보드</Link></div>{error.digest ? <small>지원용 오류 번호: {error.digest}</small> : null}</section>;
}

function classify(error: Error) {
  const value = error.message.toLowerCase();
  if (/403|permission|권한/.test(value)) return { code: "403 · ACCESS", kind: "forbidden", title: "이 작업을 볼 권한이 없습니다", description: "대표 관리자에게 필요한 권한을 요청하거나 허용된 판매자 화면으로 이동해 주세요." } as const;
  if (/409|conflict|충돌/.test(value)) return { code: "409 · CONFLICT", kind: "conflict", title: "다른 변경 내용이 먼저 저장되었습니다", description: "최신 값을 다시 읽고 내 변경과 비교한 뒤 다시 저장해 주세요. 기존 값을 덮어쓰지 않습니다." } as const;
  if (/503|unavailable|downstream|장애/.test(value)) return { code: "503 · TEMPORARY", kind: "unavailable", title: "판매자 자료 연결이 잠시 지연되고 있습니다", description: "구매자 화면과 다른 판매자 구획은 유지됩니다. 잠시 뒤 이 화면만 다시 확인해 주세요." } as const;
  return { code: "REQUEST FAILED", kind: "error", title: "판매자 자료를 불러오지 못했습니다", description: "요청 상태를 확인한 뒤 다시 시도해 주세요. 문제가 계속되면 지원용 오류 번호를 전달해 주세요." } as const;
}
