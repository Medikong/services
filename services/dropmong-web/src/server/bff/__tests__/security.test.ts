import { describe, expect, it } from "vitest";

import { createDevelopmentActor, readDevelopmentActor, signDevelopmentSession } from "@/server/bff/security";

describe("development session boundary", () => {
  it("accepts a signed development session and rejects a modified one", () => {
    const actor = createDevelopmentActor();
    const session = signDevelopmentSession(actor);

    expect(readDevelopmentActor(session)?.userId).toBe(actor.userId);
    expect(readDevelopmentActor(`${session}x`)).toBeNull();
  });
});
