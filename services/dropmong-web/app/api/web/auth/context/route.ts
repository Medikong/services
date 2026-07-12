import { getRequestActor } from "@/server/bff/auth";
import { getConfig } from "@/server/bff/config";
import { withBffJsonRoute } from "@/server/bff/route-handler";

export async function GET(request: Request) {
  return withBffJsonRoute(request, "/api/web/auth/context", async () => {
    const actor = getRequestActor(request);
    return {
      actor: actor
        ? { kind: "development-buyer", role: actor.role }
        : { kind: "anonymous" },
      developmentMocks: getConfig().developmentMocks,
    };
  });
}
