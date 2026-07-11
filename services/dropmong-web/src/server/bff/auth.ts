import "server-only";

import { cookies } from "next/headers";

import { getConfig } from "@/server/bff/config";
import { BffError } from "@/server/bff/errors";
import { developmentSessionCookieName, readDevelopmentActor } from "@/server/bff/security";
import type { DevelopmentActor } from "@/server/bff/types";

export async function getServerActor(): Promise<DevelopmentActor | null> {
  assertDevelopmentAuthAvailable();
  const cookieStore = await cookies();
  return readDevelopmentActor(cookieStore.get(developmentSessionCookieName)?.value);
}

export function getRequestActor(request: Request): DevelopmentActor | null {
  assertDevelopmentAuthAvailable();
  return readDevelopmentActor(readCookie(request.headers.get("cookie"), developmentSessionCookieName));
}

function assertDevelopmentAuthAvailable(): void {
  if (getConfig().developmentMocks) {
    return;
  }
  throw new BffError({
    code: "WEB_AUTH_CONTRACT_UNAVAILABLE",
    message: "인증 컨텍스트 API가 연결되기 전에는 로그인 상태를 확인할 수 없습니다.",
    retryable: false,
    status: 503,
  });
}

function readCookie(header: string | null, name: string): string | undefined {
  if (!header) {
    return undefined;
  }
  for (const part of header.split(";")) {
    const [key, ...value] = part.trim().split("=");
    if (key === name) {
      return value.join("=");
    }
  }
  return undefined;
}
