import "server-only";

import { describeDropAvailability, getProductWithDrop } from "@/server/bff/catalog";
import type { RequestContext } from "@/server/bff/request-context";
import type { PageMeta, ProductWithDrop } from "@/server/bff/types";

export type ProductDetailPageDto = ProductWithDrop & {
  actions: {
    canStartCheckout: boolean;
    availabilityLabel: string;
  };
  personalization: {
    status: "unavailable";
    message: string;
  };
  meta: PageMeta;
};

export async function getProductDetailPage(
  context: RequestContext,
  productId: string,
  dropId?: string,
): Promise<ProductDetailPageDto> {
  const result = await getProductWithDrop(context, productId, dropId);
  const availability = describeDropAvailability(result.drop, result.product);

  return {
    ...result,
    actions: {
      canStartCheckout: availability.canStartCheckout,
      availabilityLabel: availability.label,
    },
    personalization: {
      status: "unavailable",
      message: "관심 상품과 알림 상태는 인증 서비스 연결 후 제공됩니다.",
    },
    meta: {
      requestId: context.requestId,
      serverNow: new Date().toISOString(),
      partial: false,
    },
  };
}
