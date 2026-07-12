import { getHomePage } from "@/server/bff/home";
import { withBffJsonRoute } from "@/server/bff/route-handler";

export async function GET(request: Request) {
  return withBffJsonRoute(request, "/api/web/home", getHomePage);
}
