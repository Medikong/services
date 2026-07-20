import { withBffJsonRoute } from "@/server/bff/route-handler";

export async function GET(request: Request) {
  return withBffJsonRoute(request, "/healthz", async () => {
    return {
      status: "ok",
      service: "dropmong-web",
      timestamp: new Date().toISOString(),
    };
  });
}
