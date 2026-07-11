import { getRequestActor } from "@/server/bff/auth";
import { confirmCheckout } from "@/server/bff/checkout";
import { BffError } from "@/server/bff/errors";
import { withBffJsonRoute } from "@/server/bff/route-handler";
import { assertCsrf, assertIdempotencyKey, assertSameOrigin } from "@/server/bff/security";

type RouteContext = {
  params: Promise<{ checkoutId: string }>;
};

export async function POST(request: Request, { params }: RouteContext) {
  const { checkoutId } = await params;
  return withBffJsonRoute(request, "/api/web/checkouts/[checkoutId]/confirm", async (context) => {
    const actor = getRequestActor(request);
    if (!actor) {
      throw new BffError({
        code: "WEB_LOGIN_REQUIRED",
        message: "결제를 진행하려면 로그인이 필요합니다.",
        status: 401,
      });
    }
    assertSameOrigin(request);
    assertCsrf(request, actor);
    assertIdempotencyKey(request);
    await assertAgreementConfirmed(request);
    return confirmCheckout(context, checkoutId, actor);
  });
}

async function assertAgreementConfirmed(request: Request): Promise<void> {
  let body: unknown;
  try {
    body = await request.json();
  } catch {
    throw new BffError({
      code: "WEB_REQUEST_INVALID",
      message: "주문 동의 정보를 확인할 수 없습니다.",
      status: 400,
    });
  }
  if (
    !body ||
    typeof body !== "object" ||
    Array.isArray(body) ||
    Object.keys(body).length !== 1 ||
    (body as Record<string, unknown>).agreementConfirmed !== true
  ) {
    throw new BffError({
      code: "WEB_REQUEST_INVALID",
      message: "주문 동의가 필요합니다.",
      status: 400,
    });
  }
}
