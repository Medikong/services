import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

const sellerRoutes = [
  ["/seller", /오늘 처리할 일/],
  ["/seller/drops", "드롭 관리"],
  ["/seller/products", "상품 관리"],
  ["/seller/drops/new?step=terms", "드롭 초안"],
  ["/seller/drops/drop-001/edit?step=inventory", "드롭 초안"],
  ["/seller/drops/drop-002/review", "검수와 변경 요청"],
  ["/seller/orders", "주문과 출고 자료"],
  ["/seller/coupons", "쿠폰과 제휴"],
  ["/seller/analytics", "판매 분석"],
  ["/seller/settlements", "정산 조회"],
  ["/seller/settings/store", "판매자와 스토어 설정"],
  ["/seller/settings/members", "팀과 권한"],
  ["/seller/issues", "운영 이슈"],
] as const;

test.beforeEach(async ({ page }) => {
  await page.goto("/seller");
  const signIn = page.getByRole("link", { name: "개발용 판매자 세션 시작" });
  const dashboard = page.getByRole("heading", { level: 1, name: /오늘 처리할 일/ });
  await expect(signIn.or(dashboard)).toBeVisible();
  if (await signIn.isVisible()) await signIn.click();
  await expect(dashboard).toBeVisible();
});

test("판매자 로그인 뒤 PAGE.A.200~211 화면과 편집 경로를 모두 연다", async ({ page }) => {
  for (const [href, heading] of sellerRoutes) {
    await page.goto(href);
    await expect(page.getByRole("heading", { level: 1, name: heading })).toBeVisible();
  }
});

test("필터와 상세 패널 URL을 새로고침·닫기 뒤에도 안전하게 복원한다", async ({ page }) => {
  await page.goto("/seller/products?q=윈드&status=active&productId=product-001");
  await expect(page.getByRole("heading", { name: "product-001 상세" })).toBeVisible();
  await page.reload();
  await expect(page.getByRole("heading", { name: "product-001 상세" })).toBeVisible();
  await page.getByRole("link", { name: "패널 닫기" }).click();
  await expect(page).toHaveURL(/q=%EC%9C%88%EB%93%9C/);
  await expect(page).toHaveURL(/status=active/);
  await expect(page).not.toHaveURL(/productId/);
});

test("주문 제한 snapshot은 마스킹하고 export를 비활성화한다", async ({ page }) => {
  await page.goto("/seller/orders?state=stale");
  await expect(page.getByText("김*롭 / 010-****-1823")).toBeVisible();
  await expect(page.getByRole("button", { name: "출고 자료 요청" })).toBeDisabled();
  await expect(page.getByText(/생성·다운로드·편집 작업/)).toBeVisible();
});

test("민감한 주문 자료 요청은 재인증과 회전된 CSRF 뒤에만 처리한다", async ({ page }) => {
  await page.goto("/seller/orders");
  await page.getByRole("checkbox").check();
  await page.getByRole("button", { name: "출고 자료 요청" }).click();
  await expect(page.getByText(/본인 확인이 필요/)).toBeVisible();
  await page.getByRole("link", { name: "본인 확인 후 계속" }).click();
  await expect(page).toHaveURL(/\/seller\/orders/);
  await page.getByRole("checkbox").check();
  await page.getByRole("button", { name: "출고 자료 요청" }).click();
  await expect(page.getByText("요청을 안전하게 처리했습니다.")).toBeVisible();
});

test("판매자 대시보드는 키보드·접근성 규칙과 모든 기준 너비를 만족한다", async ({ page }) => {
  for (const width of [360, 390, 768, 1024, 1440]) {
    await page.setViewportSize({ width, height: width < 700 ? 844 : 900 });
    await page.goto("/seller");
    await expect(page.getByRole("heading", { level: 1, name: /오늘 처리할 일/ })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/seller");
  await expect(page.getByRole("heading", { level: 1, name: /오늘 처리할 일/ })).toBeVisible();
  await page.keyboard.press("Tab");
  await expect(page.locator(":focus")).toBeVisible();
  const result = await new AxeBuilder({ page }).analyze();
  expect(result.violations).toEqual([]);
});

test("onboarding 세션은 판매자와 대표 membership을 만든 뒤 설정으로 돌아온다", async ({ page }) => {
  await page.context().clearCookies();
  await page.goto("/seller");
  const href = await page.getByRole("link", { name: "개발용 판매자 세션 시작" }).getAttribute("href");
  expect(href).toBeTruthy();
  await page.goto(`${href}&mode=onboarding`);
  await expect(page.getByRole("heading", { level: 1, name: "판매자 등록" })).toBeVisible();
  await page.getByLabel("스토어 표시 이름").fill("새 드롭 스튜디오");
  await page.getByRole("button", { name: "판매자 등록 시작" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "판매자와 스토어 설정" })).toBeVisible();
});
