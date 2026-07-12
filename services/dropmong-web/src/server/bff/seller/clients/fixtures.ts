import type { SellerPageData, SellerPageKind } from "@/server/bff/seller/contracts/types";

type PageSeed = Omit<SellerPageData, "asOf" | "kind" | "panel" | "partial" | "readOnly" | "stale" | "unavailableSections" | "version">;

const commonFilters = {
  q: { key: "q", label: "검색", placeholder: "이름 또는 식별자" },
  status: { key: "status", label: "상태", options: [
    { label: "전체", value: "" }, { label: "진행 중", value: "active" }, { label: "완료", value: "done" },
  ] },
};

const seeds: Record<SellerPageKind, PageSeed> = {
  dashboard: {
    eyebrow: "SELLER OVERVIEW", title: "오늘 처리할 일을 먼저 확인하세요",
    description: "마감과 검수 기한이 가까운 작업을 판매자 범위 안에서 모았습니다.",
    metrics: [
      { label: "오늘 주문", value: "38건", note: "14:30 기준 추정" },
      { label: "판매 금액", value: "4,280,000원", note: "취소 전 추정" },
      { label: "출고 대기", value: "12건", note: "내일 14:00 마감" },
      { label: "정산 예정", value: "2,940,000원", note: "7월 18일 예정" },
    ],
    columns: [{ key: "task", label: "우선 작업" }, { key: "deadline", label: "기한" }, { key: "state", label: "상태" }, { key: "href", label: "이동" }],
    rows: [
      { task: "반려된 여름 셔츠 드롭 보완", deadline: "오늘 18:00", state: "보완 필요", href: "/seller/drops/drop-002/review" },
      { task: "출고 자료 확인", deadline: "내일 14:00", state: "12건", href: "/seller/orders?fulfillmentStatus=ready" },
      { task: "정산 보류 사유 확인", deadline: "7월 16일", state: "보류", href: "/seller/settlements?settlementId=settlement-002" },
    ], filters: [], actions: [{ href: "/seller/drops/new", label: "새 드롭 준비" }], emptyMessage: "지금 바로 처리할 작업이 없습니다.",
  },
  drops: {
    eyebrow: "DROP OPERATIONS", title: "드롭 관리", description: "초안·검수·판매 상태를 서로 다른 축으로 확인합니다.",
    metrics: [{ label: "초안", value: "3개" }, { label: "검수 중", value: "2개" }, { label: "판매 중", value: "1개" }],
    columns: [{ key: "name", label: "드롭" }, { key: "draft", label: "초안" }, { key: "review", label: "검수" }, { key: "sales", label: "판매" }],
    rows: [
      { name: "DMG 썸머 셸", draft: "저장됨", review: "승인", sales: "판매 중" },
      { name: "리넨 셔츠 2차", draft: "저장됨", review: "반려", sales: "미판매" },
      { name: "트레일 백", draft: "작성 중", review: "미제출", sales: "미판매" },
    ], filters: [commonFilters.q, { ...commonFilters.status, key: "reviewStatus", label: "검수 상태" }], actions: [{ href: "/seller/drops/new", label: "새 드롭 만들기" }], emptyMessage: "조건에 맞는 드롭이 없습니다.",
  },
  products: {
    eyebrow: "CATALOG", title: "상품 관리", description: "판매에 사용할 상품 정보와 옵션을 확인하고 수정합니다.",
    metrics: [{ label: "판매 가능", value: "18개" }, { label: "임시 저장", value: "4개" }],
    columns: [{ key: "name", label: "상품" }, { key: "category", label: "카테고리" }, { key: "price", label: "판매가" }, { key: "state", label: "상태" }],
    rows: [
      { name: "DMG 윈드 브레이커", category: "아우터", price: "89,000원", state: "판매 가능", id: "product-001" },
      { name: "드롭몽 리넨 셔츠", category: "상의", price: "62,000원", state: "검토 필요", id: "product-002" },
    ], filters: [commonFilters.q, commonFilters.status], actions: [{ commandPath: "products/save", label: "상품 저장", method: "POST", permission: "seller.product.write" }], emptyMessage: "등록한 상품이 없습니다.",
  },
  "drop-editor": {
    eyebrow: "DROP DRAFT", title: "드롭 초안", description: "상품, 판매 조건, 재고, 최종 확인을 단계별로 저장합니다.",
    metrics: [{ label: "현재 단계", value: "판매 조건" }, { label: "저장 상태", value: "임시 저장됨", note: "version draft-v3" }],
    columns: [{ key: "section", label: "단계" }, { key: "state", label: "상태" }, { key: "detail", label: "확인 사항" }],
    rows: [
      { section: "상품", state: "완료", detail: "DMG 윈드 브레이커" },
      { section: "판매 조건", state: "작성 중", detail: "가격·기간" },
      { section: "재고", state: "대기", detail: "옵션별 수량" },
      { section: "최종 확인", state: "대기", detail: "검수 제출 전 확인" },
    ], filters: [{ key: "step", label: "단계", options: [
      { label: "상품", value: "product" }, { label: "판매 조건", value: "terms" }, { label: "재고", value: "inventory" }, { label: "최종 확인", value: "review" },
    ] }], actions: [{ commandPath: "drop-drafts/save", label: "초안 저장", method: "POST", permission: "seller.drop.write" }], emptyMessage: "초안 단계가 없습니다.",
  },
  review: {
    eyebrow: "REVIEW", title: "검수와 변경 요청", description: "반려 사유를 확인하고 보완한 내용만 다시 제출합니다.",
    metrics: [{ label: "검수 상태", value: "보완 필요" }, { label: "마감", value: "오늘 18:00" }],
    columns: [{ key: "field", label: "검수 항목" }, { key: "reason", label: "사유" }, { key: "state", label: "보완 상태" }],
    rows: [{ field: "대표 이미지", reason: "제품 전체가 보이는 이미지 필요", state: "교체 완료" }, { field: "판매 기간", reason: "종료 시각 확인 필요", state: "확인 전" }],
    filters: [], actions: [{ commandPath: "reviews/submit", label: "검수 다시 제출", method: "POST", permission: "seller.drop.review.submit" }], emptyMessage: "검수 요청 내역이 없습니다.",
  },
  orders: {
    eyebrow: "FULFILLMENT", title: "주문과 출고 자료", description: "사전 마스킹된 주문 projection만 조회하며 출고 상태를 변경하지 않습니다.",
    metrics: [{ label: "출고 대기", value: "12건" }, { label: "자료 만료", value: "24시간", note: "생성 시점부터" }],
    columns: [{ key: "order", label: "주문" }, { key: "buyer", label: "구매자" }, { key: "item", label: "상품" }, { key: "state", label: "출고 자료" }],
    rows: [{ order: "ORD-240712-1038", buyer: "김*롭 / 010-****-1823", item: "윈드 브레이커 L", state: "요청 가능", id: "order-001" }, { order: "ORD-240712-1011", buyer: "박*몽 / 010-****-7742", item: "윈드 브레이커 M", state: "준비됨", id: "order-002" }],
    filters: [commonFilters.q, { ...commonFilters.status, key: "fulfillmentStatus", label: "자료 상태" }], actions: [{ commandPath: "order-exports/create", label: "출고 자료 요청", method: "POST", permission: "seller.order.export", strongAuthPurpose: "seller_order_export" }], emptyMessage: "조건에 맞는 주문이 없습니다.",
  },
  coupons: {
    eyebrow: "PROMOTION", title: "쿠폰과 제휴", description: "판매자 범위, 승인 상태와 비용 부담 주체를 함께 확인합니다.",
    metrics: [{ label: "활성 쿠폰", value: "2개" }, { label: "제휴 제안", value: "1건" }],
    columns: [{ key: "name", label: "쿠폰·제휴" }, { key: "scope", label: "적용 범위" }, { key: "cost", label: "비용 부담" }, { key: "state", label: "승인 상태" }],
    rows: [{ name: "썸머 드롭 10%", scope: "DMG 썸머 셸", cost: "판매자", state: "승인" }, { name: "크리에이터 공동 프로모션", scope: "리넨 셔츠 2차", cost: "협의 중", state: "응답 대기" }],
    filters: [commonFilters.q, commonFilters.status], actions: [{ commandPath: "coupons/save", label: "쿠폰 저장", method: "POST", permission: "seller.coupon.write" }], emptyMessage: "쿠폰이나 제휴 제안이 없습니다.",
  },
  analytics: {
    eyebrow: "ANALYTICS", title: "판매 분석", description: "실시간 추정과 집계 완료 값을 구분하며 모든 차트에 표를 함께 제공합니다.",
    metrics: [{ label: "주문 전환", value: "4.8%", note: "14:30 기준 추정" }, { label: "확정 매출", value: "18,420,000원", note: "7월 11일 집계 완료" }],
    columns: [{ key: "date", label: "날짜" }, { key: "orders", label: "주문" }, { key: "confirmed", label: "확정 매출" }, { key: "estimate", label: "실시간 추정" }],
    rows: [{ date: "7월 10일", orders: "92건", confirmed: "8,240,000원", estimate: "-" }, { date: "7월 11일", orders: "114건", confirmed: "10,180,000원", estimate: "-" }, { date: "7월 12일", orders: "38건", confirmed: "집계 전", estimate: "4,280,000원" }],
    filters: [{ key: "from", label: "시작일" }, { key: "to", label: "종료일" }, { key: "productId", label: "상품 ID" }], actions: [], emptyMessage: "선택한 기간의 집계가 없습니다.",
  },
  settlements: {
    eyebrow: "SETTLEMENT", title: "정산 조회", description: "예정·보류·확정·차감 예정 상태를 원장 값 그대로 확인합니다.",
    metrics: [{ label: "정산 예정", value: "2,940,000원" }, { label: "보류", value: "310,000원" }],
    columns: [{ key: "period", label: "판매 기간" }, { key: "amount", label: "정산 금액" }, { key: "deduction", label: "차감 예정" }, { key: "state", label: "상태" }],
    rows: [{ period: "7월 1일~7일", amount: "2,940,000원", deduction: "0원", state: "예정", id: "settlement-001" }, { period: "6월 24일~30일", amount: "310,000원", deduction: "42,000원", state: "보류", id: "settlement-002" }],
    filters: [{ key: "from", label: "시작일" }, { key: "to", label: "종료일" }, commonFilters.status], actions: [], emptyMessage: "정산 내역이 없습니다.",
  },
  store: {
    eyebrow: "SELLER SETTINGS", title: "판매자와 스토어 설정", description: "SellerAccount와 StoreProfile을 서로 다른 version과 저장 작업으로 관리합니다.",
    metrics: [{ label: "판매자 검증", value: "검증 완료" }, { label: "스토어 공개", value: "공개 중" }],
    columns: [{ key: "section", label: "구분" }, { key: "value", label: "현재 값" }, { key: "version", label: "버전" }, { key: "permission", label: "저장 권한" }],
    rows: [{ section: "SellerAccount", value: "사업자 판매자", version: "account-v8", permission: "대표 관리자" }, { section: "StoreProfile", value: "드롭몽 스튜디오", version: "store-v12", permission: "스토어 편집" }],
    filters: [], actions: [
      { commandPath: "account/save", label: "판매자 계정 저장", method: "POST", permission: "seller.account.write", strongAuthPurpose: "seller_account_change", version: "account-v8" },
      { commandPath: "store-profile/save", label: "스토어 프로필 저장", method: "POST", permission: "seller.store.write", version: "store-v12" },
    ], emptyMessage: "판매자 등록을 시작해 주세요.",
  },
  members: {
    eyebrow: "TEAM & ACCESS", title: "팀과 권한", description: "역할 이름이 아니라 서버가 확인한 permission과 version으로 작업을 제어합니다.",
    metrics: [{ label: "활성 구성원", value: "4명" }, { label: "초대 대기", value: "1명" }],
    columns: [{ key: "name", label: "구성원" }, { key: "role", label: "역할" }, { key: "access", label: "주요 권한" }, { key: "state", label: "상태" }],
    rows: [{ name: "김드롭", role: "대표 관리자", access: "전체 관리", state: "활성", id: "member-001" }, { name: "이상품", role: "상품 담당자", access: "상품·드롭", state: "활성", id: "member-002" }, { name: "최출고", role: "출고 담당자", access: "주문 조회", state: "활성", id: "member-003" }],
    filters: [commonFilters.q, commonFilters.status], actions: [
      { commandPath: "members/invite", label: "구성원 초대", method: "POST", permission: "seller.member.write", strongAuthPurpose: "seller_member_manage", version: "membership-v4" },
      { commandPath: "roles/permissions/save", label: "역할 권한표 저장", method: "POST", permission: "seller.role.permission.write", strongAuthPurpose: "seller_member_manage", version: "permissions-v7" },
    ], emptyMessage: "구성원이 없습니다.",
  },
  issues: {
    eyebrow: "OPERATIONS", title: "운영 이슈", description: "주문·드롭·정산과 연결된 문제를 신고하고 처리 상태를 확인합니다.",
    metrics: [{ label: "처리 중", value: "2건" }, { label: "이번 주 해결", value: "5건" }],
    columns: [{ key: "issue", label: "이슈" }, { key: "related", label: "관련 항목" }, { key: "opened", label: "등록 시각" }, { key: "state", label: "상태" }],
    rows: [{ issue: "출고 자료 항목 확인 요청", related: "ORD-240712-1038", opened: "오늘 13:20", state: "접수", id: "issue-001" }, { issue: "정산 보류 사유 문의", related: "6월 24일~30일", opened: "어제 16:42", state: "확인 중", id: "issue-002" }],
    filters: [commonFilters.q, { ...commonFilters.status, key: "type", label: "이슈 유형" }], actions: [{ commandPath: "issues/create", label: "운영 이슈 신고", method: "POST", permission: "seller.issue.write" }], emptyMessage: "등록된 운영 이슈가 없습니다.",
  },
};

export function getSellerPageFixture(kind: SellerPageKind, search: URLSearchParams): SellerPageData {
  const seed = seeds[kind];
  const panelId = search.get("productId") ?? search.get("orderId") ?? search.get("couponId") ?? search.get("proposalId")
    ?? search.get("settlementId") ?? search.get("memberId") ?? search.get("issueId");
  const stale = search.get("state") === "stale";
  const partial = search.get("state") === "partial";
  return {
    ...seed,
    kind,
    asOf: "2026-07-12T14:30:00+09:00",
    panel: panelId ? { id: panelId, title: `${panelId} 상세`, body: "현재 판매자 범위에서 확인된 정보입니다. 새로고침해도 이 패널과 필터가 유지됩니다." } : null,
    partial,
    readOnly: stale && kind === "orders",
    stale,
    unavailableSections: partial ? ["실시간 집계"] : [],
    version: `${kind}-v3`,
  };
}
