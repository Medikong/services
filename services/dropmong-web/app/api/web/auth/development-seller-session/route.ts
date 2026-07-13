import { NextResponse } from "next/server";

import { getConfig } from "@/server/bff/config";
import { withBffResponseRoute } from "@/server/bff/route-handler";
import {
  createDevelopmentSellerActor,
  developmentSellerSessionCookieName,
  readSellerReturnTo,
  signDevelopmentSellerSession,
} from "@/server/bff/seller/security";

export async function GET(request: Request) {
  return withBffResponseRoute(request, "/api/web/auth/development-seller-session", async () => {
    const config = getConfig();
    if (!config.developmentMocks || !config.sellerPortalEnabled) return new NextResponse(null, { status: 404 });
    const url = new URL(request.url);
    const returnTo = readSellerReturnTo(url.searchParams.get("returnToken"));
    if (!returnTo) return NextResponse.json({ code: "WEB_RETURN_TO_INVALID" }, { status: 400 });
    const mode = url.searchParams.get("mode") === "onboarding" ? "onboarding"
      : url.searchParams.get("mode") === "restricted" ? "restricted" : "active";
    const actor = createDevelopmentSellerActor(mode);
    const destination = mode === "onboarding" ? "/seller/settings/store?onboarding=1" : returnTo;
    const response = NextResponse.redirect(new URL(destination, config.appOrigin));
    response.cookies.set(developmentSellerSessionCookieName, signDevelopmentSellerSession(actor), {
      httpOnly: true, sameSite: "lax", secure: config.appOrigin.protocol === "https:", path: "/", maxAge: 60 * 60 * 2,
    });
    return response;
  });
}
