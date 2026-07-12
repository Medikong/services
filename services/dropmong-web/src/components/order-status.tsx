"use client";

import { useEffect, useState, type ReactNode } from "react";

import { Icon } from "@/components/icons";
import type { OrderStatus } from "@/server/bff/types";

type OrderStatusProps = {
  initialStatus: OrderStatus;
  orderId: string;
};

const terminalStatuses = new Set<OrderStatus>(["CONFIRMED", "PAYMENT_FAILED", "CANCELED", "EXPIRED"]);

const statusCopy: Record<OrderStatus, {
  description: string;
  icon: "check" | "clock" | "shield";
  label: string;
  title: ReactNode;
  tone: "confirmed" | "pending" | "failed";
}> = {
  CONFIRMED: {
    description: "드롭 참여가 성공적으로 완료되었습니다.",
    icon: "check",
    label: "주문이 확정되었습니다.",
    title: <>주문이 <em>완료</em>되었어요!</>,
    tone: "confirmed",
  },
  PENDING_PAYMENT: {
    description: "결제 결과를 주문 원장에 반영하고 있습니다. 새 주문은 만들지 않고 이 화면에서만 상태를 다시 확인합니다.",
    icon: "clock",
    label: "주문 상태를 확인하고 있습니다.",
    title: <>주문 <em>확정</em>을 확인하고 있어요.</>,
    tone: "pending",
  },
  PAYMENT_FAILED: {
    description: "결제가 승인되지 않았습니다. 결제는 자동으로 재시도하지 않으니 상품 상세에서 다시 주문해 주세요.",
    icon: "shield",
    label: "결제가 승인되지 않았습니다.",
    title: <>주문이 <em>완료되지 않았어요.</em></>,
    tone: "failed",
  },
  CANCELED: {
    description: "이 주문은 취소되었습니다. 필요하면 상품 상세에서 새 주문을 시작해 주세요.",
    icon: "shield",
    label: "주문이 취소되었습니다.",
    title: <>주문이 <em>취소되었어요.</em></>,
    tone: "failed",
  },
  EXPIRED: {
    description: "결제 가능한 시간이 지나 주문이 만료되었습니다. 상품 상세에서 현재 재고를 다시 확인해 주세요.",
    icon: "shield",
    label: "주문이 만료되었습니다.",
    title: <>주문 시간이 <em>만료되었어요.</em></>,
    tone: "failed",
  },
};

export function OrderStatusPanel({ initialStatus, orderId }: OrderStatusProps) {
  const [status, setStatus] = useState(initialStatus);
  const [isChecking, setIsChecking] = useState(false);
  const [checkError, setCheckError] = useState<string | null>(null);
  const [retryCount, setRetryCount] = useState(0);

  useEffect(() => {
    if (terminalStatuses.has(status)) {
      return;
    }
    let isActive = true;
    let controller: AbortController | undefined;
    let timer: number | undefined;

    async function checkOrderStatus() {
      if (document.visibilityState !== "visible") {
        return;
      }
      controller?.abort();
      controller = new AbortController();
      setIsChecking(true);
      try {
        const response = await fetch(`/api/web/orders/${encodeURIComponent(orderId)}`, { signal: controller.signal });
        const body: unknown = await response.json();
        if (!response.ok || !isOrderStatus(body)) {
          throw new Error("order status response was not usable");
        }
        if (isActive) {
          setStatus(body.status);
          setCheckError(null);
        }
      } catch (error) {
        if (isActive && !(error instanceof DOMException && error.name === "AbortError")) {
          setCheckError("주문 상태를 다시 확인하지 못했습니다. 네트워크를 확인한 뒤 다시 시도해 주세요.");
        }
      } finally {
        if (isActive) {
          setIsChecking(false);
        }
      }
    }

    function startPolling() {
      if (document.visibilityState !== "visible" || timer !== undefined) {
        return;
      }
      void checkOrderStatus();
      timer = window.setInterval(() => void checkOrderStatus(), 1500);
    }

    function stopPolling() {
      if (timer !== undefined) {
        window.clearInterval(timer);
        timer = undefined;
      }
      controller?.abort();
    }

    function handleVisibilityChange() {
      if (document.visibilityState === "visible") {
        startPolling();
      } else {
        stopPolling();
      }
    }

    startPolling();
    document.addEventListener("visibilitychange", handleVisibilityChange);
    return () => {
      isActive = false;
      stopPolling();
      document.removeEventListener("visibilitychange", handleVisibilityChange);
    };
  }, [orderId, retryCount, status]);

  const copy = statusCopy[status];

  return (
    <>
      <div className={`complete-hero__check complete-hero__check--${copy.tone}`}><Icon name={copy.icon} /></div>
      <span className="eyebrow">{copy.tone === "confirmed" ? "ORDER CONFIRMED" : "ORDER STATUS"}</span>
      <h1>{copy.title}</h1>
      <p>{copy.description}</p>
      <p aria-live="polite" className={`order-status order-status--${copy.tone}`}><Icon name={copy.icon} />{isChecking && copy.tone === "pending" ? "주문 상태를 확인하는 중입니다." : copy.label}</p>
      {checkError ? <div className="order-status__error" role="status"><span>{checkError}</span><button className="button button--secondary" onClick={() => setRetryCount((count) => count + 1)} type="button">다시 확인</button></div> : null}
    </>
  );
}

function isOrderStatus(value: unknown): value is { status: OrderStatus } {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return false;
  }
  const status = (value as Record<string, unknown>).status;
  return typeof status === "string" && ["PENDING_PAYMENT", "CONFIRMED", "PAYMENT_FAILED", "CANCELED", "EXPIRED"].includes(status);
}
