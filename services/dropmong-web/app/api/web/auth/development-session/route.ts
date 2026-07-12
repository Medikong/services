import { NextResponse } from "next/server";

import { getConfig } from "@/server/bff/config";
import { BffError } from "@/server/bff/errors";
import { withBffResponseRoute } from "@/server/bff/route-handler";
import {
  createDevelopmentActor,
  developmentSessionCookieName,
  signDevelopmentSession,
} from "@/server/bff/security";

export async function GET(request: Request) {
  return withBffResponseRoute(request, "/api/web/auth/development-session", async () => {
    const config = getConfig();
    if (!config.developmentMocks) {
      throw new BffError({
        code: "WEB_RESOURCE_NOT_FOUND",
        message: "요청한 인증 경로를 찾을 수 없습니다.",
        status: 404,
      });
    }

    const actor = createDevelopmentActor();
    const response = NextResponse.redirect(new URL(safeReturnTo(request), config.appOrigin));
    response.cookies.set({
      name: developmentSessionCookieName,
      value: signDevelopmentSession(actor),
      httpOnly: true,
      maxAge: Math.floor((actor.expiresAt - Date.now()) / 1000),
      path: "/",
      sameSite: "lax",
      secure: config.appOrigin.protocol === "https:",
    });
    return response;
  });
}

function safeReturnTo(request: Request): string {
  const rawValue = new URL(request.url).searchParams.get("returnTo") ?? "/";
  const config = getConfig();
  let destination: URL;
  try {
    destination = new URL(rawValue, config.appOrigin);
  } catch {
    return "/";
  }
  if (destination.origin !== config.appOrigin.origin) {
    return "/";
  }
  if (!destination.pathname.startsWith("/checkout") && !destination.pathname.startsWith("/orders")) {
    return "/";
  }
  return `${destination.pathname}${destination.search}`;
}
