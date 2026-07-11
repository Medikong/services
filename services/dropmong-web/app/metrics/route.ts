import { NextResponse } from "next/server";

import { metricsText } from "@/server/bff/observability";
import { withBffResponseRoute } from "@/server/bff/route-handler";

export async function GET(request: Request) {
  return withBffResponseRoute(request, "/metrics", async () =>
    new NextResponse(metricsText(), {
      headers: {
        "Cache-Control": "no-store",
        "Content-Type": "text/plain; version=0.0.4; charset=utf-8",
      },
    }),
  );
}
