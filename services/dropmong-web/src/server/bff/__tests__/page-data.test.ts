import { describe, expect, it } from "vitest";

import { BffError } from "@/server/bff/errors";
import { loadPageData } from "@/server/bff/page-data";

describe("page data error boundary", () => {
  it("converts a typed BFF failure into a renderable problem result", async () => {
    const result = await loadPageData(async () => {
      throw new BffError({
        code: "WEB_DEPENDENCY_UNAVAILABLE",
        message: "드롭 정보를 불러올 수 없습니다.",
        retryable: true,
        status: 503,
      });
    });

    expect(result).toEqual({
      ok: false,
      problem: {
        detail: "드롭 정보를 불러올 수 없습니다.",
        retryable: true,
        title: "WEB_DEPENDENCY_UNAVAILABLE",
      },
    });
  });

  it("does not conceal an unexpected programming error", async () => {
    await expect(loadPageData(async () => {
      throw new Error("unexpected");
    })).rejects.toThrow("unexpected");
  });
});
