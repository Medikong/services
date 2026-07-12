export class BffError extends Error {
  readonly code: string;
  readonly retryable: boolean;
  readonly status: number;

  constructor(options: { code: string; message: string; retryable?: boolean; status: number }) {
    super(options.message);
    this.name = "BffError";
    this.code = options.code;
    this.retryable = options.retryable ?? false;
    this.status = options.status;
  }
}

export class RecentAuthRequiredError extends BffError {
  readonly reauthenticationHref: string;

  constructor(reauthenticationHref: string) {
    super({
      code: "WEB_RECENT_AUTH_REQUIRED",
      message: "이 작업을 계속하려면 본인 확인이 필요합니다.",
      status: 403,
    });
    this.reauthenticationHref = reauthenticationHref;
  }
}

export function isBffError(error: unknown): error is BffError {
  return error instanceof BffError;
}

export function toBffError(error: unknown): BffError {
  if (isBffError(error)) {
    return error;
  }
  return new BffError({
    code: "WEB_INTERNAL_ERROR",
    message: "요청을 처리하는 중 문제가 발생했습니다. 잠시 후 다시 시도해 주세요.",
    status: 500,
  });
}
