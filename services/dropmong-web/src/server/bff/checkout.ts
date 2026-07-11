import "server-only";

import { getProductWithDrop } from "@/server/bff/catalog";
import { getConfig } from "@/server/bff/config";
import { BffError } from "@/server/bff/errors";
import type { RequestContext } from "@/server/bff/request-context";
import type { CheckoutSnapshot, DevelopmentActor, OrderResult } from "@/server/bff/types";

type DevelopmentCheckoutSeed = {
  dropId: string;
  option: "S" | "M" | "L" | "XL";
  productId: string;
  quantity: number;
};

export async function getCheckoutSnapshot(
  context: RequestContext,
  checkoutId: string,
): Promise<CheckoutSnapshot> {
  assertCheckoutMockAvailable();
  const seed = decodeDevelopmentCheckoutId(checkoutId);
  const { drop, product } = await getProductWithDrop(context, seed.productId, seed.dropId);
  const subtotal = product.price * seed.quantity;
  const canConfirm = drop.status === "OPEN" && product.remainingQuantity >= seed.quantity;

  return {
    checkoutId,
    source: "development-mock",
    item: {
      dropId: drop.id,
      productId: product.id,
      name: product.name,
      optionLabel: seed.option,
      quantity: seed.quantity,
      unitPrice: product.price,
    },
    delivery: {
      recipient: "개발 구매자",
      phone: "010-0000-0000",
      address: "서울특별시 DropMong 개발로 1",
      shippingFee: 0,
      requestedAt: "문 앞에 놓아 주세요",
    },
    paymentMethod: {
      id: "MOCK_CARD",
      label: "개발용 mock 카드",
      description: "실제 카드 정보 없이 주문 완료 화면을 검증합니다.",
    },
    benefits: {
      coupon: "쿠폰 계약 연결 대기",
      point: "포인트 계약 연결 대기",
    },
    totals: {
      subtotal,
      discount: 0,
      shippingFee: 0,
      total: subtotal,
    },
    actions: canConfirm
      ? { canConfirm: true }
      : {
          canConfirm: false,
          unavailableReason: "현재 드롭 상태 또는 서버가 확인한 재고로는 이 수량을 주문할 수 없습니다.",
        },
    asOf: new Date().toISOString(),
  };
}

export async function confirmCheckout(
  context: RequestContext,
  checkoutId: string,
  actor: DevelopmentActor,
): Promise<{ orderId: string; state: "CONFIRMED" }> {
  const snapshot = await getCheckoutSnapshot(context, checkoutId);
  if (!snapshot.actions.canConfirm) {
    throw new BffError({
      code: "WEB_STATE_CONFLICT",
      message: snapshot.actions.unavailableReason ?? "현재 주문을 진행할 수 없습니다.",
      status: 409,
    });
  }
  if (actor.role !== "CUSTOMER") {
    throw new BffError({
      code: "WEB_PERMISSION_DENIED",
      message: "구매자 권한을 확인할 수 없습니다.",
      status: 403,
    });
  }

  return {
    orderId: makeDevelopmentOrderId(checkoutId),
    state: "CONFIRMED",
  };
}

export async function getOrderResult(
  context: RequestContext,
  orderId: string,
): Promise<OrderResult> {
  assertCheckoutMockAvailable();
  const checkoutId = checkoutIdFromDevelopmentOrderId(orderId);
  const snapshot = await getCheckoutSnapshot(context, checkoutId);
  const confirmedAt = new Date().toISOString();

  return {
    id: orderId,
    status: "CONFIRMED",
    createdAt: confirmedAt,
    confirmedAt,
    amount: snapshot.totals.total,
    product: {
      name: snapshot.item.name,
      optionLabel: snapshot.item.optionLabel,
      quantity: snapshot.item.quantity,
    },
    deliveryExpectedAt: "영업일 기준 3일 이내",
    source: "development-mock",
  };
}

function assertCheckoutMockAvailable(): void {
  if (!getConfig().developmentMocks) {
    throw new BffError({
      code: "WEB_CHECKOUT_CONTRACT_UNAVAILABLE",
      message: "통합 checkout 계약이 연결되기 전에는 주문을 진행할 수 없습니다.",
      retryable: false,
      status: 503,
    });
  }
}

function decodeDevelopmentCheckoutId(checkoutId: string): DevelopmentCheckoutSeed {
  if (!checkoutId.startsWith("dev.")) {
    throw new BffError({
      code: "WEB_REQUEST_INVALID",
      message: "개발용 checkout 식별자가 올바르지 않습니다.",
      status: 400,
    });
  }
  let value: unknown;
  try {
    value = JSON.parse(Buffer.from(checkoutId.slice(4), "base64url").toString("utf8"));
  } catch {
    throw new BffError({
      code: "WEB_REQUEST_INVALID",
      message: "개발용 checkout 식별자가 올바르지 않습니다.",
      status: 400,
    });
  }
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw invalidCheckoutSeed();
  }
  const seed = value as Record<string, unknown>;
  const dropId = seed.dropId;
  const option = seed.option;
  const productId = seed.productId;
  const quantity = seed.quantity;
  if (
    typeof dropId !== "string" ||
    (option !== "S" && option !== "M" && option !== "L" && option !== "XL") ||
    typeof productId !== "string" ||
    typeof quantity !== "number" ||
    !Number.isInteger(quantity) ||
    quantity < 1 ||
    quantity > 10
  ) {
    throw invalidCheckoutSeed();
  }
  return {
    dropId,
    option,
    productId,
    quantity,
  };
}

function makeDevelopmentOrderId(checkoutId: string): string {
  return `dev-order.${checkoutId.slice(4)}`;
}

function checkoutIdFromDevelopmentOrderId(orderId: string): string {
  if (!orderId.startsWith("dev-order.")) {
    throw new BffError({
      code: "WEB_RESOURCE_NOT_FOUND",
      message: "주문을 찾을 수 없습니다.",
      status: 404,
    });
  }
  return `dev.${orderId.slice("dev-order.".length)}`;
}

function invalidCheckoutSeed(): BffError {
  return new BffError({
    code: "WEB_REQUEST_INVALID",
    message: "개발용 checkout 식별자가 올바르지 않습니다.",
    status: 400,
  });
}
