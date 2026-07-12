import { getProductDetailPage } from "@/server/bff/product";
import { withBffJsonRoute } from "@/server/bff/route-handler";

type RouteContext = {
  params: Promise<{ productId: string }>;
};

export async function GET(request: Request, { params }: RouteContext) {
  const { productId } = await params;
  const dropId = new URL(request.url).searchParams.get("dropId") ?? undefined;
  return withBffJsonRoute(request, "/api/web/products/[productId]", (context) =>
    getProductDetailPage(context, productId, dropId),
  );
}
