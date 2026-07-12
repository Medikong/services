import "server-only";

import { listDrops } from "@/server/bff/catalog";
import type { RequestContext } from "@/server/bff/request-context";
import type { Drop, PageMeta } from "@/server/bff/types";

export type HomePageDto = {
  featured: Drop | null;
  upcoming: Drop[];
  ranking: Drop[];
  personalization: {
    status: "anonymous" | "unavailable";
    message: string;
  };
  meta: PageMeta;
};

export async function getHomePage(context: RequestContext): Promise<HomePageDto> {
  const drops = await listDrops(context);
  const featured = drops.find((drop) => drop.status === "OPEN") ?? null;
  const upcoming = drops.filter((drop) => drop.status === "UPCOMING").slice(0, 3);
  const ranking = drops.filter((drop) => drop.status === "OPEN" || drop.status === "SOLD_OUT").slice(0, 3);

  return {
    featured,
    upcoming,
    ranking,
    personalization: {
      status: "anonymous",
      message: "로그인하면 드롭 알림과 관심 상품 상태를 확인할 수 있습니다.",
    },
    meta: {
      requestId: context.requestId,
      serverNow: new Date().toISOString(),
      partial: false,
    },
  };
}
