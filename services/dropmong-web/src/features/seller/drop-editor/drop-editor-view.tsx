import Link from "next/link";

import { SellerCommandCards, SellerPageHeader, SellerPageState, SellerProgress, SellerSection, SellerStatusBadge, type SellerViewProps } from "@/components/seller/seller-page-primitives";

const steps = [{ key: "product", label: "상품 정보" }, { key: "terms", label: "판매 조건" }, { key: "inventory", label: "재고" }, { key: "review", label: "검수 요청" }];

export function SellerDropEditorView({ csrfToken, page, search }: SellerViewProps) {
  const current = search.get("step") ?? "terms";
  const currentIndex = Math.max(0, steps.findIndex((step) => step.key === current));
  return <div className="seller-workspace seller-editor"><SellerPageHeader page={page} /><SellerPageState page={page} /><nav aria-label="드롭 등록 단계" className="seller-stepper">{steps.map((step, index) => <Link aria-current={index === currentIndex ? "step" : undefined} className={index < currentIndex ? "is-complete" : index === currentIndex ? "is-current" : ""} href={`?step=${step.key}`} key={step.key}><span>{index < currentIndex ? "✓" : index + 1}</span><strong>{step.label}</strong><small>{index < currentIndex ? "완료" : index === currentIndex ? "현재 단계" : "미진행"}</small></Link>)}</nav>
    <div className="seller-editor-grid"><SellerSection title={steps[currentIndex]?.label ?? "판매 조건"} description="입력 내용은 저장 전까지 브라우저 저장소에 보관하지 않습니다."><form className="seller-long-form">
      <fieldset><legend>오픈 및 종료 설정</legend><div className="seller-form-grid"><label>오픈 날짜·시각<input defaultValue="2026-07-20T14:00" type="datetime-local" /></label><label>종료 날짜·시각<input defaultValue="2026-07-27T23:59" type="datetime-local" /></label><label>구매 노출 시작<input defaultValue="2026-07-20T00:00" type="datetime-local" /></label></div></fieldset>
      <fieldset><legend>판매 조건</legend><div className="seller-form-grid"><label>판매가<span className="seller-input-suffix"><input defaultValue="89000" inputMode="numeric" min="100" type="number" /><i>원</i></span></label><label>1인 구매 제한<span className="seller-input-suffix"><input defaultValue="2" inputMode="numeric" min="1" type="number" /><i>개</i></span><small className="seller-field-error">최소 1개 이상 설정해야 합니다.</small></label><label>배송 방식<select defaultValue="delivery"><option value="delivery">배송</option><option value="pickup">현장 수령</option><option value="digital">디지털 상품</option></select></label></div></fieldset>
      <fieldset><legend>구매자 안내</legend><label>배송 정책<textarea defaultValue="결제 완료 후 영업일 기준 1~2일 이내 출고됩니다." rows={3} /></label><label>교환·환불 정책<textarea defaultValue="수령 후 7일 이내 교환·환불을 요청할 수 있습니다." rows={3} /></label></fieldset>
    </form></SellerSection><aside className="seller-editor-summary"><SellerSection title="재고 요약"><dl><div><dt>총 재고</dt><dd>1,780개</dd></div><div><dt>판매 가능</dt><dd>1,280개</dd></div><div><dt>예약 재고</dt><dd>500개</dd></div></dl><SellerProgress label="판매 가능 재고" value={72} /></SellerSection><SellerSection title="드롭 미리보기"><div className="seller-drop-preview"><span><SellerStatusBadge>오픈 예정</SellerStatusBadge></span><div aria-label="드롭 상품 이미지 미리보기" className="seller-drop-preview__art" role="img" /><h3>드롭몽 썸머 컬렉션</h3><p>89,000원 <strong>D-00 02:14:32</strong></p></div></SellerSection></aside></div>
    <div className="seller-editor-actions"><Link className="seller-button seller-button--text" href={currentIndex > 0 ? `?step=${steps[currentIndex - 1]?.key}` : "/seller/drops"}>이전</Link><SellerCommandCards csrfToken={csrfToken} page={page} title="드롭 초안 저장" /><Link className="seller-button seller-button--primary" href={`?step=${steps[Math.min(steps.length - 1, currentIndex + 1)]?.key}`}>다음</Link></div>
  </div>;
}
