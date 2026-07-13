import { SellerWorkspacePage } from "@/features/seller/seller-workspace-page";

export default async function Page({ params, searchParams }: { params: Promise<{ dropId: string }>; searchParams: Promise<Record<string, string | string[] | undefined>> }) { const { dropId } = await params; return <SellerWorkspacePage kind="drop-editor" resourceId={dropId} searchParams={searchParams} />; }
