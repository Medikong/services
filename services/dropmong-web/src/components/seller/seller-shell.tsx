import { SellerIcon } from "@/components/seller/seller-icon";
import { SellerMobileNavigation, SellerSidebar } from "@/components/seller/seller-navigation";
import type { SellerContextDto } from "@/server/bff/seller/contracts/types";

export function SellerShell({ children, context }: { children: React.ReactNode; context: SellerContextDto }) {
  return (
    <div className="seller-app">
      <SellerSidebar context={context} />
      <header className="seller-topbar">
        <SellerMobileNavigation context={context} />
        <form action="/seller" className="seller-global-search" method="get" role="search"><label className="sr-only" htmlFor="seller-global-q">판매자 포털 전체 검색</label><input id="seller-global-q" name="q" placeholder="주문, 상품, 드롭명을 검색하세요" /><button aria-label="검색" type="submit"><SellerIcon name="search" /></button></form>
        <div className="seller-topbar__actions">
          <a aria-label="도움말" className="seller-icon-button seller-help-button" href="/seller/issues?type=help"><SellerIcon name="help" /></a>
          <button aria-label="읽지 않은 알림 3개" className="seller-icon-button seller-notification" type="button"><SellerIcon name="bell" /><span>3</span></button>
        </div>
        <div className="seller-context">
          <span aria-hidden="true">{context.seller?.displayName?.slice(0, 1) ?? "D"}</span>
          <div><strong>{context.seller?.displayName ?? "판매자 등록"}</strong><small>{context.seller?.verificationStatus ?? "등록 준비"} · {context.membership?.roleLabel ?? "온보딩"}</small></div>
          <details><summary aria-label="사용자 메뉴"><SellerIcon name="chevron" /></summary><div><strong>{context.actor.displayName}</strong><span>{context.membership?.roleLabel ?? "등록 준비"}</span><Link href="/seller/settings/store">판매자 정보</Link><Link href="/">구매자 홈</Link></div></details>
        </div>
      </header>
      <main className="seller-main" id="main-content">{children}</main>
    </div>
  );
}
import Link from "next/link";
