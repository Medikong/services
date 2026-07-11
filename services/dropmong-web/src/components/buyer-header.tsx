import Link from "next/link";

import { Icon } from "@/components/icons";

type BuyerHeaderProps = {
  backHref?: string;
  title?: string;
};

export function BuyerHeader({ backHref, title }: BuyerHeaderProps) {
  return (
    <header className="buyer-header">
      <div className="buyer-header__inner">
        {backHref ? (
          <Link aria-label="이전 페이지" className="icon-button" href={backHref}>
            <Icon name="arrow-left" />
          </Link>
        ) : (
          <span className="buyer-header__spacer" />
        )}
        {title ? (
          <h1 className="buyer-header__title">{title}</h1>
        ) : (
          <Link aria-label="DropMong 홈" className="brand" href="/">
            DropMong<span aria-hidden="true">+</span>
          </Link>
        )}
        <span aria-label="구매자 웹" className="buyer-header__mode">BUYER</span>
      </div>
    </header>
  );
}
