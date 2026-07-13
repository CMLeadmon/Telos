import { test, expect, type Page } from "@playwright/test";

// Credentialed run against a live gateway with the seeded P&P book:
//   E2E_USERNAME=<user> E2E_PASSWORD=<pass> npx playwright test library
const USERNAME = process.env.E2E_USERNAME;
const PASSWORD = process.env.E2E_PASSWORD;

test.skip(!USERNAME || !PASSWORD, "E2E_USERNAME/E2E_PASSWORD not set");

async function login(page: Page) {
  await page.goto("/login/");
  await page.locator("#username").fill(USERNAME!);
  await page.locator("#password").fill(PASSWORD!);
  await page.getByRole("button", { name: "Enter the node" }).click();
  await page.waitForURL("**/chat/**");
}

test("library lists the catalog with real covers and facets", async ({
  page,
}) => {
  await login(page);
  await page.goto("/library/");
  const card = page
    .getByTestId("library-card")
    .filter({ hasText: "Pride and Prejudice" });
  await expect(card).toBeVisible({ timeout: 15_000 });
  // Real cover, not a broken image (mock fallback would 503 the cover route).
  const img = card.locator("img");
  await expect
    .poll(async () => img.evaluate((el: HTMLImageElement) => el.naturalWidth))
    .toBeGreaterThan(0);
  // Facet filtering narrows and clears.
  await page.getByRole("button", { name: /Jane Austen/ }).click();
  await expect(page.getByTestId("library-card")).toHaveCount(1);
});

test("epub reader opens, paginates and persists progress", async ({
  page,
}) => {
  test.setTimeout(90_000); // 24 MB epub fetch + locations.generate on CI-slow hosts
  await login(page);
  await page.goto("/library/");
  await page.getByLabel("read Pride and Prejudice").click();
  const reader = page.getByTestId("book-reader");
  await expect(reader).toBeVisible();
  // 24 MB EPUB: allow generous open time.
  await expect(page.getByTestId("reader-percent")).not.toHaveText("0%", {
    timeout: 60_000,
  });
  const before = await page.getByTestId("reader-percent").textContent();
  // epubjs's next() is async and drops overlapping calls — wait for each
  // page turn to settle before firing the next one.
  for (let i = 0; i < 6; i++) {
    await page.getByLabel("next page").click();
    await page.waitForTimeout(300);
  }
  await expect(page.getByTestId("reader-percent")).not.toHaveText(before!, {
    timeout: 15_000,
  });
  // Debounced save is 1s; give it breathing room, then verify restore.
  await page.waitForTimeout(2_000);
  await page.getByLabel("close reader").click();
  await page.reload();
  await page.getByLabel("read Pride and Prejudice").click();
  await expect(page.getByTestId("reader-percent")).not.toHaveText("0%", {
    timeout: 60_000,
  });
});
