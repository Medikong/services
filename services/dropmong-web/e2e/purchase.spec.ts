import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

test("홈에서 개발용 구매자 세션을 거쳐 주문 완료까지 이동한다", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: /DMG 윈드 브레이커/ })).toBeVisible();
  await page.getByRole("link", { name: /드롭 자세히 보기/ }).click();

  await expect(page.getByRole("heading", { name: /DMG 윈드 브레이커/ })).toBeVisible();
  await page.getByRole("button", { exact: true, name: "L" }).click();
  await page.getByRole("button", { name: "수량 늘리기" }).click();
  await page.getByRole("link", { name: /바로 구매하기/ }).click();

  await expect(page.getByRole("heading", { name: /로그인이 필요/ })).toBeVisible();
  await page.getByRole("link", { name: "개발용 구매자 세션 시작" }).click();

  await expect(page.getByRole("heading", { name: "주문 / 결제" })).toBeVisible();
  await expect(page.getByText("옵션 L · 상품 2개")).toBeVisible();
  await page.getByRole("checkbox").check();
  await page.getByRole("button", { name: /결제하기/ }).click();

  await expect(page.getByRole("heading", { name: /주문이 완료/ })).toBeVisible();
  await expect(page.getByText("주문이 확정되었습니다.")).toBeVisible();
  await expect(page.getByText("개발용 mock").last()).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});

test("홈은 기본 접근성 규칙을 만족한다", async ({ page }) => {
  await page.goto("/");
  const result = await new AxeBuilder({ page }).analyze();
  expect(result.violations).toEqual([]);
});
