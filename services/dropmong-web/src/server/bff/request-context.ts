import "server-only";

import { randomUUID } from "node:crypto";

export type RequestContext = {
  method: string;
  requestId: string;
  route: string;
  traceparent: string;
};

const requestIdPattern = /^[A-Za-z0-9._:-]{1,128}$/;
const traceparentPattern = /^00-[0-9a-f]{32}-[0-9a-f]{16}-0[01]$/;

export function createRequestContext(headers: Pick<Headers, "get">, route: string, method = "GET"): RequestContext {
  const receivedRequestId = headers.get("x-request-id");
  const receivedTraceparent = headers.get("traceparent");

  return {
    method,
    requestId: receivedRequestId && requestIdPattern.test(receivedRequestId) ? receivedRequestId : randomUUID(),
    route,
    traceparent: receivedTraceparent && traceparentPattern.test(receivedTraceparent)
      ? receivedTraceparent
      : createTraceparent(),
  };
}

function createTraceparent(): string {
  const traceId = randomUUID().replaceAll("-", "");
  const spanId = randomUUID().replaceAll("-", "").slice(0, 16);
  return `00-${traceId}-${spanId}-01`;
}
