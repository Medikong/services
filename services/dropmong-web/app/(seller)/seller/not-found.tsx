import Link from "next/link";

export default function SellerNotFound() {
  return <section className="seller-boundary"><span>NOT FOUND</span><h1>요청한 판매자 정보를 찾을 수 없습니다</h1><p>정보가 없거나 현재 판매자 범위에 속하지 않습니다.</p><Link className="seller-button seller-button--primary" href="/seller">대시보드로 이동</Link></section>;
}
