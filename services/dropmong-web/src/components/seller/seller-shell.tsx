import Link from "next/link";

import type { SellerContextDto } from "@/server/bff/seller/contracts/types";

export function SellerShell({ children, context }: { children: React.ReactNode; context: SellerContextDto }) {
  return (
    <div className="seller-app">
      <header className="seller-topbar">
        <Link className="seller-brand" href="/seller" aria-label="DropMong 판매자 포털 홈">
          <span aria-hidden="true">DM</span><strong>DropMong</strong><small>SELLER</small>
        </Link>
        <div className="seller-context">
          <span>{context.seller?.displayName ?? "판매자 등록"}</span>
          <strong>{context.actor.displayName}</strong>
          <small>{context.membership?.roleLabel ?? "등록 준비"}</small>
        </div>
      </header>
      <div className="seller-frame">
        <aside className="seller-sidebar" aria-label="판매자 메뉴">
          <p className="seller-sidebar__label">WORKSPACE</p>
          <nav>{context.navigation.map((item) => <Link href={item.href} key={item.href}>{item.label}</Link>)}</nav>
          <div className="seller-sidebar__trust"><span>보안 범위</span><strong>서버에서 확인됨</strong><small>{context.seller?.verificationStatus ?? "등록 전"}</small></div>
        </aside>
        <details className="seller-mobile-nav">
          <summary>판매자 메뉴 열기</summary>
          <nav>{context.navigation.map((item) => <Link href={item.href} key={item.href}>{item.label}</Link>)}</nav>
        </details>
        <main className="seller-main" id="main-content">{children}</main>
      </div>
    </div>
  );
}
