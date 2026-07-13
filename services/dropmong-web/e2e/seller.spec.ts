import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

const sellerRoutes = [
  ["/seller", /오늘 처리할 일/, "확인 필요한 작업"],
  ["/seller/drops", "드롭 관리", "재고 소진"],
  ["/seller/products", "상품 관리", "SKU·옵션"],
  ["/seller/drops/new?step=terms", "드롭 초안", "오픈 및 종료 설정"],
  ["/seller/drops/drop-001/edit?step=inventory", "드롭 초안", "재고 요약"],
  ["/seller/drops/drop-002/review", "검수와 변경 요청", "변경 전후 비교"],
  ["/seller/orders", "주문과 출고 자료", "다운로드 목적"],
  ["/seller/coupons", "쿠폰과 제휴", "비용 부담"],
  ["/seller/analytics", "판매 분석", "판매 퍼널"],
  ["/seller/settlements", "정산 조회", "쿠폰 부담"],
  ["/seller/settings/store", "판매자와 스토어 설정", "구매자 화면 미리보기"],
  ["/seller/settings/members", "팀과 권한", "역할별 권한"],
  ["/seller/issues", "운영 이슈", "운영 이슈 목록"],
] as const;

test.beforeEach(async ({ page }) => {
  await page.goto("/seller");
  const signIn = page.getByRole("link", { name: "개발용 판매자 세션 시작" });
  const dashboard = page.getByRole("heading", { level: 1, name: /오늘 처리할 일/ });
  await expect(signIn.or(dashboard)).toBeVisible();
  if (await signIn.isVisible()) await signIn.click();
  await expect(dashboard).toBeVisible();
});

test("판매자 로그인 뒤 PAGE.A.200~211 전용 구성을 모두 연다", async ({ page }) => {
  for (const [href, heading, composition] of sellerRoutes) {
    await page.goto(href);
    await expect(page.getByRole("heading", { level: 1, name: heading })).toBeVisible();
    await expect(page.getByText(composition, { exact: false }).first()).toBeVisible();
    await expect(page.locator(".seller-logo img")).toHaveAttribute("alt", "DropMong");
  }
});

test("필터와 상세 패널 URL을 새로고침·닫기 뒤에도 안전하게 복원한다", async ({ page }) => {
  await page.goto("/seller/products?q=윈드&status=active&productId=product-001");
  await expect(page.getByRole("heading", { name: "product-001 상세" })).toBeVisible();
  await page.reload();
  await expect(page.getByRole("heading", { name: "product-001 상세" })).toBeVisible();
  const closeButton = page.getByRole("button", { name: "상세 패널 닫기" });
  await expect(closeButton).toBeFocused();
  await closeButton.click();
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
  await page.getByLabel("이 작업의 범위와 영향을 확인했습니다.").check();
  await page.getByRole("button", { name: "출고 자료 요청" }).click();
  await expect(page.getByText(/본인 확인이 필요/)).toBeVisible();
  await page.getByRole("link", { name: "본인 확인 후 계속" }).click();
  await expect(page).toHaveURL(/\/seller\/orders/);
  await page.getByLabel("이 작업의 범위와 영향을 확인했습니다.").check();
  await page.getByRole("button", { name: "출고 자료 요청" }).click();
  await expect(page.getByText("요청을 안전하게 처리했습니다.")).toBeVisible();
});

test("판매자 화면은 모든 기준 너비에서 페이지 가로 스크롤 없이 재배치된다", async ({ page }) => {
  test.setTimeout(120_000);
  for (const width of [360, 390, 768, 1024, 1440]) {
    await page.setViewportSize({ width, height: width < 700 ? 844 : 900 });
    for (const [href, heading] of sellerRoutes) {
      await page.goto(href);
      await expect(page.getByRole("heading", { level: 1, name: heading })).toBeVisible();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    }
  }
});

test("모바일 내비게이션은 초점을 가두고 닫은 뒤 메뉴 버튼으로 돌려준다", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/seller");
  const trigger = page.getByRole("button", { name: "판매자 메뉴 열기" });
  await trigger.click();
  await expect(page.getByRole("dialog", { name: "판매자 메뉴" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(trigger).toBeFocused();
});

test("키보드·접근성·reduced motion 규칙을 만족한다", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/seller");
  await expect(page.getByRole("heading", { level: 1, name: /오늘 처리할 일/ })).toBeVisible();
  await page.waitForLoadState("networkidle");
  await page.keyboard.press("Tab");
  await expect(page.locator(":focus")).toBeVisible();
  const duration = await page.locator(".seller-button").first().evaluate((element) => getComputedStyle(element).transitionDuration);
  expect(Number.parseFloat(duration)).toBeLessThanOrEqual(0.001);
  const result = await new AxeBuilder({ page }).analyze();
  expect(result.violations).toEqual([]);
});

test("빈 상태·필터 결과 없음·partial·stale 상태를 서로 다르게 표시한다", async ({ page }) => {
  await page.goto("/seller/products?q=empty");
  await expect(page.getByText("조건에 맞는 결과가 없습니다")).toBeVisible();
  await page.goto("/seller/analytics?state=partial");
  await expect(page.getByText("일부 자료만 표시합니다.")).toBeVisible();
  await page.goto("/seller/orders?state=stale");
  await expect(page.getByText("읽기 전용 snapshot")).toBeVisible();
});

test("403과 다른 판매자·미존재 리소스의 동일 404를 구분해 안내한다", async ({ page }) => {
  await page.goto("/seller/drops/missing-42/edit");
  await expect(page.getByRole("heading", { name: "요청한 판매자 정보를 찾을 수 없습니다" })).toBeVisible();
  await page.goto("/seller/drops/foreign-42/edit");
  await expect(page.getByRole("heading", { name: "요청한 판매자 정보를 찾을 수 없습니다" })).toBeVisible();
  await page.context().clearCookies();
  await page.goto("/seller");
  const href = await page.getByRole("link", { name: "개발용 판매자 세션 시작" }).getAttribute("href");
  await page.goto(`${href}&mode=restricted`);
  await page.goto("/seller/drops/new");
  await expect(page.getByRole("heading", { name: "이 작업을 볼 권한이 없습니다" })).toBeVisible();
});

test("409 충돌은 덮어쓰지 않고 최신 값 재조회 안내를 표시한다", async ({ page }) => {
  await page.route("**/api/web/seller/products/save", (route) => route.fulfill({
    contentType: "application/problem+json",
    status: 409,
    body: JSON.stringify({ code: "WEB_STATE_CONFLICT", status: 409, title: "다른 작업에서 정보가 변경되었습니다." }),
  }));
  await page.goto("/seller/products");
  await page.getByRole("button", { name: "상품 저장" }).click();
  await expect(page.getByText(/최신 값을 다시 불러온 뒤 변경 내용을 비교/)).toBeVisible();
});

test("상세 패널은 URL로 복원되고 이슈 타임라인·메모·첨부를 실제 구성으로 제공한다", async ({ page }) => {
  await page.goto("/seller/issues?issueId=issue-001");
  await expect(page.getByRole("heading", { name: "이슈 상세" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "처리 타임라인" })).toBeVisible();
  await expect(page.getByLabel("판매자 메모")).toBeVisible();
  await expect(page.getByText("출고확인서.pdf · 284KB")).toBeVisible();
  await page.reload();
  await expect(page.getByRole("heading", { name: "이슈 상세" })).toBeVisible();
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
