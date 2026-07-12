"use client";

import { useRouter } from "next/navigation";
import { useEffect, useRef } from "react";

import { SellerIcon } from "@/components/seller/seller-icon";

export function SellerDetailDrawer({ children, closeHref, panelId, title }: { children: React.ReactNode; closeHref: string; panelId: string; title: string }) {
  const router = useRouter();
  const drawerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    drawerRef.current?.querySelector<HTMLElement>("button, a, input, select, textarea")?.focus();
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") { close(); return; }
      if (event.key !== "Tab" || !drawerRef.current) return;
      const focusable = Array.from(drawerRef.current.querySelectorAll<HTMLElement>("a,button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled])"));
      const first = focusable[0]; const last = focusable.at(-1);
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
    }
    document.addEventListener("keydown", onKeyDown);
    return () => { document.body.style.overflow = previousOverflow; document.removeEventListener("keydown", onKeyDown); };
  });

  function close() {
    document.querySelector<HTMLElement>(`[data-panel-trigger="${CSS.escape(panelId)}"]`)?.focus();
    router.replace(closeHref, { scroll: false });
  }

  return <div className="seller-drawer-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) close(); }} role="presentation"><div aria-labelledby="seller-drawer-title" aria-modal="true" className="seller-detail-drawer" ref={drawerRef} role="dialog"><header><div><span>상세 정보</span><h2 id="seller-drawer-title">{title}</h2></div><button aria-label="상세 패널 닫기" className="seller-icon-button" onClick={close} type="button"><SellerIcon name="x" /></button></header>{children}</div></div>;
}
