import Link from "next/link";

import { SellerIcon } from "@/components/seller/seller-icon";

export function SellerTypedProblem({ code, message, retryHref, status }: { code: string; message: string; retryHref: string; status: number }) {
  const state = presentation(status);
  return <section className={`seller-boundary seller-boundary--${state.kind}`}><span>{status} · {code}</span><div className="seller-boundary__icon"><SellerIcon name={state.kind === "forbidden" ? "user" : "issue"} /></div><h1>{state.title}</h1><p>{state.description}</p><div><Link className="seller-button seller-button--primary" href={retryHref}>다시 확인</Link><Link className="seller-button seller-button--secondary" href="/seller">판매자 대시보드</Link></div><small>{message}</small></section>;
}

function presentation(status: number) {
  if (status === 403) return { kind: "forbidden", title: "이 작업을 볼 권한이 없습니다", description: "대표 관리자에게 필요한 권한을 요청하거나 허용된 판매자 화면으로 이동해 주세요." } as const;
  if (status === 409) return { kind: "conflict", title: "다른 변경 내용이 먼저 저장되었습니다", description: "최신 값을 다시 읽고 내 변경과 비교한 뒤 다시 저장해 주세요. 기존 값을 덮어쓰지 않습니다." } as const;
  return { kind: "unavailable", title: "판매자 자료 연결이 잠시 지연되고 있습니다", description: "구매자 화면과 다른 판매자 구획은 유지됩니다. 잠시 뒤 이 화면만 다시 확인해 주세요." } as const;
}
