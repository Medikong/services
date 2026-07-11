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
