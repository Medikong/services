import Link from "next/link";

type PageProblemProps = {
  detail: string;
  retryHref?: string;
  title: string;
};

export function PageProblem({ detail, retryHref, title }: PageProblemProps) {
  return (
    <main className="page-problem page-shell">
      <span className="eyebrow">DROP STATUS</span>
      <h1>{title}</h1>
      <p>{detail}</p>
      <div className="page-problem__actions">
        {retryHref ? <Link className="button button--primary" href={retryHref}>다시 시도하기</Link> : null}
        <Link className="button button--secondary" href="/">홈으로 가기</Link>
      </div>
    </main>
  );
}
