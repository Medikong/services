import { NextResponse } from "next/server";

import { getConfig } from "@/server/bff/config";
import { withBffResponseRoute } from "@/server/bff/route-handler";
import { getRequestSellerActor } from "@/server/bff/seller/context";
import {
  developmentSellerSessionCookieName,
  readSellerReturnTo,
  rotateDevelopmentSellerAuth,
  signDevelopmentSellerSession,
} from "@/server/bff/seller/security";

const allowedPurposes = new Set(["seller_order_export", "seller_member_manage", "seller_account_change"]);

export async function GET(request: Request) {
  return withBffResponseRoute(request, "/api/web/auth/development-seller-reauth", async () => {
    const config = getConfig();
    if (!config.developmentMocks) return new NextResponse(null, { status: 404 });
    const url = new URL(request.url);
    const purpose = url.searchParams.get("purpose") ?? "";
    const returnTo = readSellerReturnTo(url.searchParams.get("returnToken"));
    if (!allowedPurposes.has(purpose) || !returnTo) return NextResponse.json({ code: "WEB_REAUTH_INTENT_INVALID" }, { status: 400 });
    const actor = rotateDevelopmentSellerAuth(getRequestSellerActor(request), purpose);
    const response = NextResponse.redirect(new URL(returnTo, config.appOrigin));
    response.cookies.set(developmentSellerSessionCookieName, signDevelopmentSellerSession(actor), {
      httpOnly: true, sameSite: "lax", secure: config.appOrigin.protocol === "https:", path: "/", maxAge: 60 * 60 * 2,
    });
    return response;
  });
}
