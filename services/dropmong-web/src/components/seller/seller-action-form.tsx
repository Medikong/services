"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";

type Problem = { title?: string; reauthentication?: { href?: string } };

export function SellerActionForm({
  actionPath, csrfToken, label, readOnly, strongAuthPurpose, version,
}: {
  actionPath: string;
  csrfToken: string;
  label: string;
  readOnly: boolean;
  strongAuthPurpose?: string;
  version: string;
}) {
  const router = useRouter();
  const [confirmed, setConfirmed] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const [reauthenticationHref, setReauthenticationHref] = useState<string | null>(null);

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setPending(true); setMessage(null); setReauthenticationHref(null);
    try {
      const response = await fetch(`/api/web/seller/${actionPath}`, {
        method: "POST",
        headers: { "content-type": "application/json", "idempotency-key": crypto.randomUUID(), "x-csrf-token": csrfToken },
        body: JSON.stringify({ confirmed: strongAuthPurpose ? confirmed : true, version }),
      });
      const body: unknown = await response.json();
      if (!response.ok) {
        const problem = isProblem(body) ? body : {};
        setMessage(problem.title ?? "요청을 처리하지 못했습니다.");
        setReauthenticationHref(problem.reauthentication?.href ?? null);
        return;
      }
      setMessage("요청을 안전하게 처리했습니다.");
      router.refresh();
    } catch {
      setMessage("네트워크를 확인한 뒤 다시 시도해 주세요.");
    } finally { setPending(false); }
  }

  return (
    <form className="seller-action-form" onSubmit={submit}>
      {strongAuthPurpose ? (
        <label className="seller-confirm"><input checked={confirmed} onChange={(event) => setConfirmed(event.target.checked)} required type="checkbox" /> 이 작업의 범위와 영향을 확인했습니다.</label>
      ) : null}
      <button className="seller-button seller-button--primary" disabled={pending || readOnly} type="submit">{pending ? "처리 중…" : label}</button>
      {readOnly ? <p className="seller-form-message">제한 상태에서는 읽기만 할 수 있습니다.</p> : null}
      {message ? <p aria-live="polite" className="seller-form-message">{message}</p> : null}
      {reauthenticationHref ? <a className="seller-button seller-button--secondary" href={reauthenticationHref}>본인 확인 후 계속</a> : null}
    </form>
  );
}

export function SellerOnboardingForm({ csrfToken }: { csrfToken: string }) {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault(); setPending(true); setError(null);
    const form = new FormData(event.currentTarget);
    try {
      const response = await fetch("/api/web/seller/onboarding", {
        method: "POST",
        headers: { "content-type": "application/json", "idempotency-key": crypto.randomUUID(), "x-csrf-token": csrfToken },
        body: JSON.stringify({ displayName: form.get("displayName"), sellerType: form.get("sellerType") }),
      });
      const body: unknown = await response.json();
      if (!response.ok) { setError(isProblem(body) ? body.title ?? "등록을 시작하지 못했습니다." : "등록을 시작하지 못했습니다."); return; }
      window.location.assign("/seller/settings/store");
    } catch { setError("네트워크를 확인한 뒤 다시 시도해 주세요."); }
    finally { setPending(false); }
  }
  return (
    <form className="seller-onboarding-form" onSubmit={submit}>
      <label>스토어 표시 이름<input name="displayName" required minLength={2} /></label>
      <label>판매자 유형<select defaultValue="BUSINESS" name="sellerType"><option value="BUSINESS">사업자 판매자</option><option value="PROFESSIONAL">전문 셀러</option><option value="RESELLER">리셀러</option></select></label>
      <p>판매자 계정과 대표 관리자 membership을 만든 뒤 StoreProfile은 별도로 저장합니다.</p>
      <button className="seller-button seller-button--primary" disabled={pending} type="submit">{pending ? "등록 중…" : "판매자 등록 시작"}</button>
      {error ? <p aria-live="polite" className="seller-form-message">{error}</p> : null}
    </form>
  );
}

function isProblem(value: unknown): value is Problem {
  return Boolean(value && typeof value === "object" && !Array.isArray(value));
}
