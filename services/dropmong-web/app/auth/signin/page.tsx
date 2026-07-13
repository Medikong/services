import type { Metadata } from "next";
import Link from "next/link";

import { getConfig } from "@/server/bff/config";
import { readSellerReturnTo } from "@/server/bff/seller/security";

export const metadata: Metadata = { title: "로그인 | DropMong" };
export const dynamic = "force-dynamic";

export default async function SignInPage({ searchParams }: { searchParams: Promise<Record<string, string | string[] | undefined>> }) {
  const params = await searchParams;
  const token = typeof params.returnToken === "string" ? params.returnToken : null;
  const sellerReturnTo = readSellerReturnTo(token);
  const config = getConfig();
  const sellerHref = token ? `/api/web/auth/development-seller-session?returnToken=${encodeURIComponent(token)}` : null;
  return (
    <main className="signin-page page-shell" id="main-content">
      <section className="signin-card">
        <span className="eyebrow">SECURE SIGN IN</span>
        <h1>DropMong에 로그인</h1>
        <p>{sellerReturnTo ? "판매자 포털로 돌아가려면 로그인해 주세요." : "구매와 판매 업무에 사용하는 공통 로그인 화면입니다."}</p>
        {config.developmentMocks ? (
          <div className="signin-card__actions">
            {sellerHref ? <a className="button button--primary" href={sellerHref}>개발용 판매자 세션 시작</a> : null}
            <Link className="button button--secondary" href="/">구매자 홈으로 돌아가기</Link>
          </div>
        ) : <p className="seller-alert seller-alert--error">Auth 로그인 계약이 연결되지 않아 현재 로그인할 수 없습니다.</p>}
        <p className="signin-card__note">브라우저 저장소에는 token, 개인정보나 업무 상태를 저장하지 않습니다.</p>
      </section>
    </main>
  );
}
