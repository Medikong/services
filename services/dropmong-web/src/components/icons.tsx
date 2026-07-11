import type { SVGProps } from "react";

type IconName =
  | "arrow-left"
  | "arrow-right"
  | "bag"
  | "bell"
  | "check"
  | "chevron"
  | "clock"
  | "credit-card"
  | "heart"
  | "package"
  | "shield"
  | "truck";

type IconProps = SVGProps<SVGSVGElement> & {
  name: IconName;
};

export function Icon({ name, ...props }: IconProps) {
  return (
    <svg aria-hidden="true" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.9" viewBox="0 0 24 24" {...props}>
      {name === "arrow-left" && <path d="m15 18-6-6 6-6" />}
      {name === "arrow-right" && <path d="m9 18 6-6-6-6" />}
      {name === "bag" && <path d="M5 8h14l-1 11H6L5 8Zm4 0a3 3 0 0 1 6 0" />}
      {name === "bell" && <><path d="M18 9a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9Z" /><path d="M10 22h4" /></>}
      {name === "check" && <path d="m5 12 4.2 4.2L19 6.5" />}
      {name === "chevron" && <path d="m9 18 6-6-6-6" />}
      {name === "clock" && <><circle cx="12" cy="12" r="8.5" /><path d="M12 7v5l3.2 2" /></>}
      {name === "credit-card" && <><rect x="3" y="5" width="18" height="14" rx="2" /><path d="M3 10h18" /></>}
      {name === "heart" && <path d="M20.8 8.6c0 5.1-8.8 10.2-8.8 10.2S3.2 13.7 3.2 8.6A4.6 4.6 0 0 1 12 6.8a4.6 4.6 0 0 1 8.8 1.8Z" />}
      {name === "package" && <><path d="m4 7 8-4 8 4-8 4-8-4Z" /><path d="M4 7v10l8 4 8-4V7" /><path d="M12 11v10" /></>}
      {name === "shield" && <path d="M12 3 4.5 6v5.4c0 4.5 3 7.7 7.5 9.6 4.5-1.9 7.5-5.1 7.5-9.6V6L12 3Z" />}
      {name === "truck" && <><path d="M3 6h11v10H3zM14 9h3l3 3v4h-6z" /><circle cx="7" cy="18" r="1.5" /><circle cx="17" cy="18" r="1.5" /></>}
    </svg>
  );
}
