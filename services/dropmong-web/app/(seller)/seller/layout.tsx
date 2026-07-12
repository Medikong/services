import type { Metadata } from "next";
import { notFound, redirect } from "next/navigation";

import { SellerShell } from "@/components/seller/seller-shell";
import { getConfig } from "@/server/bff/config";
import { getServerSellerActor, toSellerContext } from "@/server/bff/seller/context";
import { signSellerReturnTo } from "@/server/bff/seller/security";

export const metadata: Metadata = {
  title: { default: "판매자 포털 | DropMong", template: "%s | DropMong Seller" },
  description: "DropMong 판매 준비와 운영 현황을 확인하는 판매자 포털",
  robots: { index: false, follow: false },
};

export const dynamic = "force-dynamic";

export default async function SellerLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  if (!getConfig().sellerPortalEnabled) notFound();
  const actor = await getServerSellerActor();
  if (!actor) redirect(`/auth/signin?returnToken=${encodeURIComponent(signSellerReturnTo("/seller"))}`);
  return <SellerShell context={toSellerContext(actor)}>{children}</SellerShell>;
}
