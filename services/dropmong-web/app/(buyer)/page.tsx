import Link from "next/link";
import { headers } from "next/headers";

import { BuyerHeader } from "@/components/buyer-header";
import { Countdown } from "@/components/countdown";
import { formatKrw } from "@/components/format";
import { Icon } from "@/components/icons";
import { ProductArt } from "@/components/product-art";
import { getHomePage } from "@/server/bff/home";
import { createRequestContext } from "@/server/bff/request-context";
import type { Drop } from "@/server/bff/types";

export default async function HomePage() {
  const context = createRequestContext(await headers(), "/");
  const page = await getHomePage(context);

  return (
    <main id="main-content">
      <BuyerHeader />
      <div className="page-shell home-page">
        {page.featured ? <FeaturedDrop drop={page.featured} serverNow={page.meta.serverNow} /> : <FeaturedEmpty />}
        <section aria-labelledby="upcoming-heading" className="content-section">
          <div className="section-heading"><div><span className="eyebrow">NEXT</span><h2 id="upcoming-heading">오픈 예정</h2></div><span>{page.upcoming.length}개 드롭</span></div>
          {page.upcoming.length > 0 ? (
            <div className="upcoming-grid">
              {page.upcoming.map((drop, index) => <DropCard drop={drop} index={index} key={drop.id} />)}
            </div>
          ) : (
            <div className="empty-card"><p>새로운 드롭을 준비하고 있습니다.</p><span>다음 공개 일정을 곧 안내할게요.</span></div>
          )}
        </section>
        <section aria-labelledby="ranking-heading" className="content-section">
          <div className="section-heading"><div><span className="eyebrow">LIVE NOW</span><h2 id="ranking-heading">지금 주목받는 드롭</h2></div></div>
          {page.ranking.length > 0 ? (
            <ol className="ranking-list">
              {page.ranking.map((drop, index) => {
                const product = drop.products[0];
                if (!product) return null;
                return (
                  <li key={drop.id}>
                    <Link href={productHref(drop, product.id)}>
                      <span className="rank-number">{index + 1}</span>
                      <ProductArt tone={index === 0 ? "dark" : index === 1 ? "gray" : "purple"} />
                      <span className="ranking-list__info"><strong>{product.name}</strong><small>{formatKrw(product.price)}</small></span>
                      <Icon name="arrow-right" />
                    </Link>
                  </li>
                );
              })}
            </ol>
          ) : (
            <div className="empty-card"><p>집계할 드롭이 아직 없습니다.</p><span>새 드롭이 열리면 여기에서 확인할 수 있어요.</span></div>
          )}
        </section>
        <section className="personalization-card" aria-label="개인화 안내">
          <div className="personalization-card__icon"><Icon name="bell" /></div>
          <div><strong>드롭 알림을 놓치지 마세요</strong><p>{page.personalization.message}</p></div>
          <span className="status-pill">연결 준비 중</span>
        </section>
      </div>
    </main>
  );
}

function FeaturedDrop({ drop, serverNow }: { drop: Drop; serverNow: string }) {
  const product = drop.products[0];
  if (!product) return <FeaturedEmpty />;

  return (
    <section className="featured-drop" aria-labelledby="featured-title">
      <div className="featured-drop__content">
        <span className="featured-drop__eyebrow"><Icon name="check" /> 오늘의 드롭</span>
        <div className="featured-drop__time"><span>D-00</span><Countdown endsAt={drop.closesAt} serverNow={serverNow} /></div>
        <h1 id="featured-title">{product.name}</h1>
        <p>{formatKrw(product.price)}</p>
        <Link className="button button--light" href={productHref(drop, product.id)}>드롭 자세히 보기 <Icon name="arrow-right" /></Link>
      </div>
      <ProductArt className="featured-drop__art" tone="dark" />
    </section>
  );
}

function FeaturedEmpty() {
  return (
    <section className="featured-empty">
      <span className="eyebrow">DROP STATUS</span>
      <h1>공개된 드롭을 준비하고 있어요.</h1>
      <p>드롭 정보가 등록되면 이 자리에서 바로 확인할 수 있습니다.</p>
    </section>
  );
}

function DropCard({ drop, index }: { drop: Drop; index: number }) {
  const product = drop.products[0];
  if (!product) return null;
  const tone = index === 0 ? "gray" : index === 1 ? "dark" : "purple";
  return (
    <Link className="drop-card" href={productHref(drop, product.id)}>
      <div className="drop-card__top"><span className="status-chip">D-{String(index + 1).padStart(2, "0")}</span><time>{new Intl.DateTimeFormat("ko-KR", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" }).format(new Date(drop.opensAt))}</time></div>
      <ProductArt tone={tone} />
      <strong>{product.name}</strong>
      <span>{formatKrw(product.price)}</span>
    </Link>
  );
}

function productHref(drop: Drop, productId: string): string {
  return `/products/${encodeURIComponent(productId)}?dropId=${encodeURIComponent(drop.id)}`;
}
