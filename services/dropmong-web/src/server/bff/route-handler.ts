import "server-only";

import { NextResponse } from "next/server";

import { RecentAuthRequiredError, toBffError } from "@/server/bff/errors";
import { recordRequest, traceIdFromTraceparent } from "@/server/bff/observability";
import { createRequestContext, type RequestContext } from "@/server/bff/request-context";

type BffRouteHandler = (context: RequestContext) => Promise<unknown>;
type BffResponseHandler = (context: RequestContext) => Promise<NextResponse>;

export async function withBffJsonRoute(
  request: Request,
  route: string,
  handler: BffRouteHandler,
): Promise<NextResponse> {
  return withBffResponseRoute(request, route, async (context) => {
    const body = await handler(context);
    return NextResponse.json(body, {
      headers: responseHeaders(context),
    });
  });
}

export async function withBffResponseRoute(
  request: Request,
  route: string,
  handler: BffResponseHandler,
): Promise<NextResponse> {
  const context = createRequestContext(request.headers, route, request.method);
  const startedAt = performance.now();
  let response: NextResponse;

  try {
    response = await handler(context);
    for (const [name, value] of Object.entries(responseHeaders(context))) {
      if (!response.headers.has(name)) {
        response.headers.set(name, value);
      }
    }
  } catch (error) {
    const problem = toBffError(error);
    response = NextResponse.json(
      {
        type: `https://dropmong.example/problems/${problem.code.toLowerCase()}`,
        title: problem.message,
        status: problem.status,
        code: problem.code,
        traceId: traceIdFromTraceparent(context.traceparent),
        retryable: problem.retryable,
        ...(problem instanceof RecentAuthRequiredError
          ? { reauthentication: { href: problem.reauthenticationHref } }
          : {}),
      },
      {
        status: problem.status,
        headers: responseHeaders(context),
      },
    );
  }

  recordRequest(context, response.status, Math.round(performance.now() - startedAt));
  return response;
}

export function responseHeaders(context: RequestContext): Record<string, string> {
  return {
    "Cache-Control": "no-store",
    "X-Request-Id": context.requestId,
    traceparent: context.traceparent,
  };
}
