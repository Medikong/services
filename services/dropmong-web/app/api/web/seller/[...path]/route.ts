import { NextResponse } from "next/server";

import { getConfig } from "@/server/bff/config";
import { BffError } from "@/server/bff/errors";
import { withBffJsonRoute, withBffResponseRoute } from "@/server/bff/route-handler";
import { getRequestSellerActor, toSellerContext } from "@/server/bff/seller/context";
import { executeSellerCommand } from "@/server/bff/seller/modules/commands";
import {
  createDevelopmentSellerActor,
  developmentSellerSessionCookieName,
  signDevelopmentSellerSession,
} from "@/server/bff/seller/security";

type RouteProps = { params: Promise<{ path: string[] }> };

export async function GET(request: Request, { params }: RouteProps) {
  const path = (await params).path.join("/");
  return withBffJsonRoute(request, "/api/web/seller/[...path]", async () => {
    if (path !== "context") throw notFound();
    return toSellerContext(getRequestSellerActor(request));
  });
}

export async function POST(request: Request, { params }: RouteProps) {
  const path = (await params).path.join("/");
  if (path === "onboarding") {
    return withBffResponseRoute(request, "/api/web/seller/onboarding", async (context) => {
      const actor = getRequestSellerActor(request);
      if (actor.membership || !actor.onboardingAllowed) throw new BffError({ code: "WEB_PERMISSION_DENIED", message: "판매자 등록을 시작할 수 없습니다.", status: 403 });
      const { assertSellerMutation } = await import("@/server/bff/seller/security");
      assertSellerMutation(request, actor);
      await request.json().catch(() => { throw new BffError({ code: "WEB_REQUEST_INVALID", message: "등록 정보가 올바르지 않습니다.", status: 400 }); });
      if (!getConfig().developmentMocks) throw new BffError({ code: "WEB_SELLER_CONTRACT_UNAVAILABLE", message: "판매자 등록 계약이 연결되지 않았습니다.", status: 503 });
      const activeActor = createDevelopmentSellerActor("active");
      const response = NextResponse.json({ message: "판매자 등록을 시작했습니다.", operationId: context.requestId, status: "completed", version: "account-v1" });
      response.cookies.set(developmentSellerSessionCookieName, signDevelopmentSellerSession(activeActor), {
        httpOnly: true, sameSite: "lax", secure: getConfig().appOrigin.protocol === "https:", path: "/", maxAge: 60 * 60 * 2,
      });
      return response;
    });
  }
  return withBffJsonRoute(request, "/api/web/seller/[...path]", async (context) => executeSellerCommand(request, context, getRequestSellerActor(request), path));
}

function notFound(): BffError {
  return new BffError({ code: "WEB_RESOURCE_NOT_FOUND", message: "요청한 작업을 찾을 수 없습니다.", status: 404 });
}
