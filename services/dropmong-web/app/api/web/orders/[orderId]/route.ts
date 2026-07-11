import { getRequestActor } from "@/server/bff/auth";
import { getOrderResult } from "@/server/bff/checkout";
import { BffError } from "@/server/bff/errors";
import { withBffJsonRoute } from "@/server/bff/route-handler";

type RouteContext = {
  params: Promise<{ orderId: string }>;
};

export async function GET(request: Request, { params }: RouteContext) {
  const { orderId } = await params;
  return withBffJsonRoute(request, "/api/web/orders/[orderId]", async (context) => {
    if (!getRequestActor(request)) {
      throw new BffError({
        code: "WEB_LOGIN_REQUIRED",
        message: "주문을 확인하려면 로그인이 필요합니다.",
        status: 401,
      });
    }
    return getOrderResult(context, orderId);
  });
}
