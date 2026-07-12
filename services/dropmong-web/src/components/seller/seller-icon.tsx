import type { SVGProps } from "react";

export type SellerIconName =
  | "analytics" | "bell" | "calendar" | "chevron" | "coupon" | "dashboard"
  | "drop" | "help" | "issue" | "menu" | "orders" | "package" | "search"
  | "settlement" | "store" | "team" | "user" | "x";

export function SellerIcon({ name, ...props }: SVGProps<SVGSVGElement> & { name: SellerIconName }) {
  return (
    <svg aria-hidden="true" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.8" viewBox="0 0 24 24" {...props}>
      {name === "dashboard" && <><path d="M4 10.5 12 4l8 6.5" /><path d="M6.5 9.5V20h11V9.5M10 20v-6h4v6" /></>}
      {name === "drop" && <><rect x="5" y="4" width="14" height="17" rx="2" /><path d="M9 4V2h6v2M9 9h6M9 13h6M9 17h4" /></>}
      {name === "package" && <><path d="m4 7 8-4 8 4-8 4-8-4Z" /><path d="M4 7v10l8 4 8-4V7M12 11v10" /></>}
      {name === "orders" && <><path d="M3 6h11v10H3zM14 9h3l3 3v4h-6z" /><circle cx="7" cy="18" r="1.5" /><circle cx="17" cy="18" r="1.5" /></>}
      {name === "coupon" && <><path d="M4 7a2 2 0 0 0 2-2h12a2 2 0 0 0 2 2v2a3 3 0 0 0 0 6v2a2 2 0 0 0-2 2H6a2 2 0 0 0-2-2v-2a3 3 0 0 0 0-6V7Z" /><path d="M12 7v10" /></>}
      {name === "analytics" && <><path d="M5 20V10M12 20V4M19 20v-7" /><path d="M3 20h18" /></>}
      {name === "settlement" && <><circle cx="12" cy="12" r="9" /><path d="M8 8h8M8 12h8M10 8l2 8 2-8" /></>}
      {name === "store" && <><path d="M4 9h16l-1.5-5h-13L4 9Z" /><path d="M5 9v11h14V9M9 20v-6h6v6" /><path d="M4 9a3 3 0 0 0 5 2 3 3 0 0 0 6 0 3 3 0 0 0 5-2" /></>}
      {name === "team" && <><circle cx="9" cy="8" r="3" /><circle cx="17" cy="9" r="2" /><path d="M3.5 20c.4-4 2.2-6 5.5-6s5.1 2 5.5 6M15 14c3.2 0 5 1.7 5.5 5" /></>}
      {name === "issue" && <><path d="M12 3 3.5 6v5.5c0 4.6 3.4 7.8 8.5 9.5 5.1-1.7 8.5-4.9 8.5-9.5V6L12 3Z" /><path d="M12 8v5M12 17h.01" /></>}
      {name === "search" && <><circle cx="10.5" cy="10.5" r="6.5" /><path d="m16 16 5 5" /></>}
      {name === "bell" && <><path d="M18 9a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9ZM10 22h4" /></>}
      {name === "help" && <><circle cx="12" cy="12" r="9" /><path d="M9.7 9a2.5 2.5 0 1 1 3.2 2.4c-.9.4-.9 1.1-.9 2M12 17h.01" /></>}
      {name === "user" && <><circle cx="12" cy="8" r="3.5" /><path d="M5 21c.6-4.4 2.8-6.5 7-6.5s6.4 2.1 7 6.5" /></>}
      {name === "calendar" && <><rect x="3" y="5" width="18" height="16" rx="2" /><path d="M8 3v4M16 3v4M3 10h18" /></>}
      {name === "menu" && <path d="M4 7h16M4 12h16M4 17h16" />}
      {name === "x" && <path d="m6 6 12 12M18 6 6 18" />}
      {name === "chevron" && <path d="m9 6 6 6-6 6" />}
    </svg>
  );
}
