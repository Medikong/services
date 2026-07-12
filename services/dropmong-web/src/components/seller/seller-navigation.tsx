"use client";

import Image from "next/image";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useRef, useState } from "react";

import { SellerIcon, type SellerIconName } from "@/components/seller/seller-icon";
import type { SellerContextDto } from "@/server/bff/seller/contracts/types";

const iconByHref: Record<string, SellerIconName> = {
  "/seller": "dashboard", "/seller/drops": "drop", "/seller/products": "package",
  "/seller/orders": "orders", "/seller/coupons": "coupon", "/seller/analytics": "analytics",
  "/seller/settlements": "settlement", "/seller/issues": "issue",
  "/seller/settings/store": "store", "/seller/settings/members": "team",
};

export function SellerLogo() {
  return <Link className="seller-logo" href="/seller" aria-label="DropMong 판매자 포털 홈"><Image alt="DropMong" height={44} priority src="/seller/dropmong-logo.png" width={174} /><span>SELLER</span></Link>;
}

export function SellerSidebar({ context }: { context: SellerContextDto }) {
  const pathname = usePathname();
  return (
    <aside className="seller-sidebar">
      <SellerLogo />
      <nav aria-label="판매자 메뉴" className="seller-nav">
        {context.navigation.map((item) => <SellerNavLink active={isActive(pathname, item.href)} href={item.href} key={item.href} label={item.label} />)}
      </nav>
      <Link className="seller-help-card" href="/seller/issues?type=help"><span><SellerIcon name="help" />도움이 필요하신가요?</span><small>판매 가이드와 운영 문의를 확인하세요.</small><strong>가이드 보기 <span aria-hidden="true">→</span></strong></Link>
    </aside>
  );
}

export function SellerMobileNavigation({ context }: { context: SellerContextDto }) {
  const pathname = usePathname();
  const [open, setOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const drawerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const trigger = triggerRef.current;
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    const firstLink = drawerRef.current?.querySelector<HTMLElement>("a");
    firstLink?.focus();
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") { setOpen(false); return; }
      if (event.key !== "Tab" || !drawerRef.current) return;
      const focusable = Array.from(drawerRef.current.querySelectorAll<HTMLElement>("a,button:not([disabled])"));
      const first = focusable[0]; const last = focusable.at(-1);
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
    }
    document.addEventListener("keydown", onKeyDown);
    return () => { document.body.style.overflow = previousOverflow; document.removeEventListener("keydown", onKeyDown); trigger?.focus(); };
  }, [open]);

  return (
    <>
      <button aria-expanded={open} aria-label="판매자 메뉴 열기" className="seller-icon-button seller-mobile-trigger" onClick={() => setOpen(true)} ref={triggerRef} type="button"><SellerIcon name="menu" /></button>
      {open ? <div className="seller-mobile-overlay" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) setOpen(false); }}>
        <div aria-label="판매자 메뉴" aria-modal="true" className="seller-mobile-drawer" ref={drawerRef} role="dialog">
          <div className="seller-mobile-drawer__head"><SellerLogo /><button aria-label="판매자 메뉴 닫기" className="seller-icon-button" onClick={() => setOpen(false)} type="button"><SellerIcon name="x" /></button></div>
          <nav className="seller-nav">{context.navigation.map((item) => <SellerNavLink active={isActive(pathname, item.href)} href={item.href} key={item.href} label={item.label} onClick={() => setOpen(false)} />)}</nav>
        </div>
      </div> : null}
    </>
  );
}

function SellerNavLink({ active, href, label, onClick }: { active: boolean; href: string; label: string; onClick?: () => void }) {
  return <Link aria-current={active ? "page" : undefined} className={active ? "is-active" : undefined} href={href} onClick={onClick}><SellerIcon name={iconByHref[href] ?? "dashboard"} /><span>{label}</span>{href !== "/seller" ? <SellerIcon className="seller-nav__chevron" name="chevron" /> : null}</Link>;
}

function isActive(pathname: string, href: string): boolean {
  if (href === "/seller") return pathname === href;
  return pathname === href || pathname.startsWith(`${href}/`);
}
