import { test, expect, type Page } from "@playwright/test";

// Product-truth browser assertions: no prohibited Oracle/AI surface and no inert
// advertised control (notification bell before P5-T3; My List / Watch Party
// controls before their tasks) renders in the shipped UI.

const USERNAME = process.env.E2E_USERNAME;
const PASSWORD = process.env.E2E_PASSWORD;

test.skip(!USERNAME || !PASSWORD, "E2E_USERNAME/E2E_PASSWORD not set");

test.use({ viewport: { width: 1280, height: 800 } });

async function login(page: Page) {
  await page.goto("/login/");
  await page.locator("#username").fill(USERNAME!);
  await page.locator("#password").fill(PASSWORD!);
  await page.getByRole("button", { name: "Enter the node" }).click();
  await page.waitForURL("**/chat/**");
}

test("no Oracle or AI control renders in the app shell", async ({ page }) => {
  await login(page);
  await expect(page.locator(".chan", { hasText: "general" })).toBeVisible();

  await expect(page.locator('[aria-label="oracle"]')).toHaveCount(0);
  await expect(page.getByText(/oracle/i)).toHaveCount(0);
  await expect(page.getByText(/AI[- ]powered|AI summ|ask the oracle/i)).toHaveCount(0);
});

test("the notification bell is a real control, not an inert one", async ({ page }) => {
  await login(page);
  await expect(page.locator(".chan", { hasText: "general" })).toBeVisible();
  // Shipped in P5-T3: the bell exists and opens a working inbox dialog.
  const bell = page.locator('[aria-label="notifications"]');
  await expect(bell).toBeVisible();
  await bell.click();
  await expect(page.getByRole("dialog", { name: "Notifications" })).toBeVisible();
});

test("no inert My List or Watch Party control renders yet", async ({ page }) => {
  await login(page);
  await expect(page.locator(".chan", { hasText: "general" })).toBeVisible();
  await expect(page.getByRole("button", { name: /my list/i })).toHaveCount(0);
  await expect(page.getByRole("button", { name: /watch party/i })).toHaveCount(0);
});

test("the public landing page makes no AI or absolute-privacy claim", async ({ page }) => {
  await page.goto("/");
  const body = (await page.textContent("body")) ?? "";
  expect(body).not.toMatch(/AI[- ]powered|powered by AI|AI summ|oracle/i);
  expect(body).not.toMatch(/nothing ever leaves|fully offline|100% private|completely private/i);
});
