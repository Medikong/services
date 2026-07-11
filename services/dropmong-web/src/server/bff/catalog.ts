import "server-only";

import { BffError } from "@/server/bff/errors";
import { getConfig } from "@/server/bff/config";
import type { Drop, DropStatus, Product, ProductWithDrop } from "@/server/bff/types";
import type { RequestContext } from "@/server/bff/request-context";

type CatalogListResponse = {
  data: Drop[];
  pageInfo: {
    hasNext: boolean;
    nextCursor: string | null;
  };
};

const developmentDrops: Drop[] = [
  {
    id: "drop-001",
    title: "DMG 퍼플 라인 리미티드 드롭",
    status: "OPEN",
    opensAt: "2026-07-11T01:00:00.000Z",
    closesAt: "2026-07-31T14:00:00.000Z",
    description: "퍼플 포인트로 완성한 한정판 드롭 패션 아이템입니다.",
    products: [
      {
        id: "product-001",
        name: "DMG 윈드 브레이커 퍼플 라인",
        price: 89000,
        remainingQuantity: 42,
      },
    ],
  },
  {
    id: "drop-002",
    title: "DMG 후디 그레이 오픈 예정",
    status: "UPCOMING",
    opensAt: "2026-07-12T01:00:00.000Z",
    closesAt: null,
    description: "다음 드롭에서 공개될 편안한 데일리 후디입니다.",
    products: [
      {
        id: "product-002",
        name: "DMG 후디 그레이",
        price: 79000,
        remainingQuantity: 100,
      },
    ],
  },
  {
    id: "drop-003",
    title: "DMG 볼캡 퍼플 오픈 예정",
    status: "UPCOMING",
    opensAt: "2026-07-13T01:00:00.000Z",
    closesAt: null,
    description: "가벼운 포인트를 더하는 퍼플 볼캡입니다.",
    products: [
      {
        id: "product-003",
        name: "DMG 볼캡 퍼플",
        price: 39000,
        remainingQuantity: 50,
      },
    ],
  },
  {
    id: "drop-sold-out-001",
    title: "DMG 아카이브 품절 드롭",
    status: "SOLD_OUT",
    opensAt: "2026-07-01T01:00:00.000Z",
    closesAt: "2026-07-08T14:00:00.000Z",
    description: "품절 상태를 확인하는 개발용 시나리오입니다.",
    products: [
      {
        id: "product-sold-out-001",
        name: "DMG 컨커런시 키트",
        price: 50000,
        remainingQuantity: 0,
      },
    ],
  },
];

export async function listDrops(context: RequestContext): Promise<Drop[]> {
  const config = getConfig();
  if (!config.catalogBaseUrl) {
    if (config.developmentMocks) {
      return developmentDrops.map(cloneDrop);
    }
    throw new BffError({
      code: "WEB_DEPENDENCY_UNAVAILABLE",
      message: "드롭 정보를 불러올 수 없습니다. 잠시 후 다시 시도해 주세요.",
      retryable: true,
      status: 503,
    });
  }

  const response = await fetchDownstream(
    new URL("/drops?limit=100", config.catalogBaseUrl),
    context,
  );
  const body = await parseJson(response, "catalog-service");
  return parseCatalogList(body).data;
}

export async function getProductWithDrop(
  context: RequestContext,
  productId: string,
  dropId?: string,
): Promise<ProductWithDrop> {
  const normalizedProductId = normalizeId(productId, "상품");
  const normalizedDropId = dropId ? normalizeId(dropId, "드롭") : undefined;
  const config = getConfig();

  if (normalizedDropId && config.catalogBaseUrl) {
    const response = await fetchDownstream(
      new URL(`/drops/${encodeURIComponent(normalizedDropId)}`, config.catalogBaseUrl),
      context,
    );
    const body = await parseJson(response, "catalog-service");
    const drop = parseCatalogDrop(body);
    const product = drop.products.find((candidate) => candidate.id === normalizedProductId);
    if (product) {
      return { drop, product };
    }
  }

  const drops = await listDrops(context);
  for (const drop of drops) {
    if (normalizedDropId && drop.id !== normalizedDropId) {
      continue;
    }
    const product = drop.products.find((candidate) => candidate.id === normalizedProductId);
    if (product) {
      return { drop, product };
    }
  }

  throw new BffError({
    code: "WEB_RESOURCE_NOT_FOUND",
    message: "요청한 상품을 찾을 수 없습니다.",
    status: 404,
  });
}

export function describeDropAvailability(drop: Drop, product: Product): {
  canStartCheckout: boolean;
  label: string;
} {
  if (drop.status === "UPCOMING") {
    return { canStartCheckout: false, label: "오픈 전" };
  }
  if (drop.status === "SOLD_OUT" || product.remainingQuantity === 0) {
    return { canStartCheckout: false, label: "품절" };
  }
  if (drop.status === "CLOSED") {
    return { canStartCheckout: false, label: "종료" };
  }
  return { canStartCheckout: true, label: "구매 가능 여부는 결제 전 다시 확인합니다" };
}

async function fetchDownstream(url: URL, context: RequestContext): Promise<Response> {
  let response: Response;
  try {
    response = await fetch(url, {
      cache: "no-store",
      headers: {
        "X-Request-Id": context.requestId,
        traceparent: context.traceparent,
      },
      signal: AbortSignal.timeout(700),
    });
  } catch {
    throw new BffError({
      code: "WEB_DEPENDENCY_UNAVAILABLE",
      message: "드롭 정보를 불러올 수 없습니다. 잠시 후 다시 시도해 주세요.",
      retryable: true,
      status: 503,
    });
  }

  if (response.status === 404) {
    throw new BffError({
      code: "WEB_RESOURCE_NOT_FOUND",
      message: "요청한 드롭을 찾을 수 없습니다.",
      status: 404,
    });
  }
  if (!response.ok) {
    throw new BffError({
      code: "WEB_DOWNSTREAM_CONTRACT_INVALID",
      message: "드롭 정보를 불러올 수 없습니다. 잠시 후 다시 시도해 주세요.",
      retryable: response.status >= 500,
      status: response.status >= 500 ? 503 : 502,
    });
  }
  return response;
}

async function parseJson(response: Response, downstreamService: string): Promise<unknown> {
  try {
    return await response.json();
  } catch {
    throw new BffError({
      code: "WEB_DOWNSTREAM_CONTRACT_INVALID",
      message: `${downstreamService} 응답 형식을 확인할 수 없습니다.`,
      status: 502,
    });
  }
}

function parseCatalogList(value: unknown): CatalogListResponse {
  const record = asRecord(value);
  const rawDrops = record.data;
  if (!Array.isArray(rawDrops)) {
    throw invalidCatalogResponse();
  }
  return {
    data: rawDrops.map(parseDrop),
    pageInfo: { hasNext: false, nextCursor: null },
  };
}

function parseCatalogDrop(value: unknown): Drop {
  const record = asRecord(value);
  return parseDrop(record.data);
}

function parseDrop(value: unknown): Drop {
  const record = asRecord(value);
  const status = parseDropStatus(record.status);
  const products = record.products;
  if (
    typeof record.id !== "string" ||
    typeof record.title !== "string" ||
    typeof record.opensAt !== "string" ||
    !Array.isArray(products)
  ) {
    throw invalidCatalogResponse();
  }
  return {
    id: record.id,
    title: record.title,
    status,
    opensAt: record.opensAt,
    closesAt: typeof record.closesAt === "string" ? record.closesAt : null,
    description: typeof record.description === "string" ? record.description : undefined,
    products: products.map(parseProduct),
  };
}

function parseProduct(value: unknown): Product {
  const record = asRecord(value);
  if (
    typeof record.id !== "string" ||
    typeof record.name !== "string" ||
    typeof record.price !== "number" ||
    typeof record.remainingQuantity !== "number"
  ) {
    throw invalidCatalogResponse();
  }
  return {
    id: record.id,
    name: record.name,
    price: record.price,
    remainingQuantity: record.remainingQuantity,
  };
}

function parseDropStatus(value: unknown): DropStatus {
  if (value === "UPCOMING" || value === "OPEN" || value === "SOLD_OUT" || value === "CLOSED") {
    return value;
  }
  throw invalidCatalogResponse();
}

function asRecord(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw invalidCatalogResponse();
  }
  return value as Record<string, unknown>;
}

function invalidCatalogResponse(): BffError {
  return new BffError({
    code: "WEB_DOWNSTREAM_CONTRACT_INVALID",
    message: "드롭 정보 응답 형식이 올바르지 않습니다.",
    status: 502,
  });
}

function normalizeId(value: string, label: string): string {
  const normalized = value.trim();
  if (!normalized || normalized.length > 128) {
    throw new BffError({
      code: "WEB_REQUEST_INVALID",
      message: `${label} 식별자가 올바르지 않습니다.`,
      status: 400,
    });
  }
  return normalized;
}

function cloneDrop(drop: Drop): Drop {
  return {
    ...drop,
    products: drop.products.map((product) => ({ ...product })),
  };
}
