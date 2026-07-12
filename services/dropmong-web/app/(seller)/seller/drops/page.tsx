import { SellerWorkspacePage } from "@/features/seller/seller-workspace-page";

export default function Page({ searchParams }: { searchParams: Promise<Record<string, string | string[] | undefined>> }) { return <SellerWorkspacePage kind="drops" searchParams={searchParams} />; }
