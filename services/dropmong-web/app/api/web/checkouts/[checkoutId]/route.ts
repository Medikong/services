import { getRequestActor } from "@/server/bff/auth";
import { getCheckoutSnapshot } from "@/server/bff/checkout";
import { BffError } from "@/server/bff/errors";
import { withBffJsonRoute } from "@/server/bff/route-handler";

type RouteContext = {
  params: Promise<{ checkoutId: string }>;
};

export async function GET(request: Request, { params }: RouteContext) {
  const { checkoutId } = await params;
  return withBffJsonRoute(request, "/api/web/checkouts/[checkoutId]", async (context) => {
    if (!getRequestActor(request)) {
      throw new BffError({
        code: "WEB_LOGIN_REQUIRED",
        message: "주문을 계속하려면 로그인이 필요합니다.",
        status: 401,
      });
    }
    return getCheckoutSnapshot(context, checkoutId);
  });
}
