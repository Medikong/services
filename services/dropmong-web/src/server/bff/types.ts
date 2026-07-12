export type DropStatus = "UPCOMING" | "OPEN" | "SOLD_OUT" | "CLOSED";

export type Product = {
  id: string;
  name: string;
  price: number;
  remainingQuantity: number;
};

export type Drop = {
  id: string;
  title: string;
  status: DropStatus;
  opensAt: string;
  closesAt: string | null;
  description?: string;
  products: Product[];
};

export type ProductWithDrop = {
  product: Product;
  drop: Drop;
};

export type PageMeta = {
  requestId: string;
  serverNow: string;
  partial: boolean;
};

export type CheckoutSnapshot = {
  checkoutId: string;
  source: "development-mock";
  item: {
    dropId: string;
    productId: string;
    name: string;
    optionLabel: string;
    quantity: number;
    unitPrice: number;
  };
  delivery: {
    recipient: string;
    phone: string;
    address: string;
    shippingFee: number;
    requestedAt: string;
  };
  paymentMethod: {
    id: "MOCK_CARD";
    label: string;
    description: string;
  };
  benefits: {
    coupon: string;
    point: string;
  };
  totals: {
    subtotal: number;
    discount: number;
    shippingFee: number;
    total: number;
  };
  actions: {
    canConfirm: boolean;
    unavailableReason?: string;
  };
  asOf: string;
};

export type OrderStatus = "PENDING_PAYMENT" | "CONFIRMED" | "PAYMENT_FAILED" | "CANCELED" | "EXPIRED";

export type OrderResult = {
  id: string;
  status: OrderStatus;
  createdAt: string;
  confirmedAt: string | null;
  amount: number;
  product: {
    name: string;
    optionLabel: string;
    quantity: number;
  };
  deliveryExpectedAt: string;
  source: "development-mock";
};

export type DevelopmentActor = {
  userId: string;
  role: "CUSTOMER";
  csrfToken: string;
  expiresAt: number;
};
