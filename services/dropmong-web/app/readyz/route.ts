import { getConfig } from "@/server/bff/config";
import { withBffJsonRoute } from "@/server/bff/route-handler";

export async function GET(request: Request) {
  return withBffJsonRoute(request, "/readyz", async () => {
    getConfig();
    return {
      status: "ready",
      service: "dropmong-web",
      checks: {
        configuration: "ok",
        bff: "ok",
      },
      timestamp: new Date().toISOString(),
    };
  });
}
