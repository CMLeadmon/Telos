import { test, expect, type Page } from "@playwright/test";

// Requires a live stack with at least one PDF book in the library.
const USERNAME = process.env.E2E_USERNAME;
const PASSWORD = process.env.E2E_PASSWORD;

test.skip(!USERNAME || !PASSWORD, "E2E_USERNAME/E2E_PASSWORD not set");

test.use({ viewport: { width: 1280, height: 900 } });

async function login(page: Page) {
  await page.goto("/login/");
  await page.locator("#username").fill(USERNAME!);
  await page.locator("#password").fill(PASSWORD!);
  await page.getByRole("button", { name: "Enter the node" }).click();
  await page.waitForURL("**/chat/**");
}

test("a PDF opens in-app on a canvas with page controls, not a new tab", async ({ page, context }) => {
  await login(page);
  await page.goto("/library/");

  // Find a PDF book card; skip if the seeded library has none.
  const pdfCard = page.locator('[data-format="PDF"]').first();
  if ((await pdfCard.count()) === 0) test.skip(true, "no PDF book in the library");

  const pagesBefore = context.pages().length;
  await pdfCard.click();
  await pdfCard.getByRole("button", { name: /read/i }).click().catch(() => {});

  const reader = page.getByTestId("book-reader");
  await expect(reader).toBeVisible();
  // In-app: the PDF reader canvas renders; no extra tab was opened.
  await expect(page.getByTestId("pdf-reader")).toBeVisible();
  await expect(page.getByTestId("pdf-canvas")).toBeVisible();
  expect(context.pages().length).toBe(pagesBefore);

  // Page navigation advances the page indicator.
  const info = page.getByTestId("pdf-pageinfo");
  await expect(info).toContainText("1 /");
  await page.getByRole("button", { name: "next page" }).click();
  await expect(info).toContainText("2 /");
});
