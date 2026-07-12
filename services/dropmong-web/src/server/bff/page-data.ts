import "server-only";

import { isBffError } from "@/server/bff/errors";

export type PageDataResult<T> =
  | { ok: true; value: T }
  | { ok: false; problem: { detail: string; retryable: boolean; title: string } };

export async function loadPageData<T>(load: () => Promise<T>): Promise<PageDataResult<T>> {
  try {
    return { ok: true, value: await load() };
  } catch (error) {
    if (isBffError(error)) {
      return {
        ok: false,
        problem: {
          detail: error.message,
          retryable: error.retryable,
          title: error.code,
        },
      };
    }
    throw error;
  }
}
