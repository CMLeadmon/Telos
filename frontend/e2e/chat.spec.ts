import { test, expect, type Page } from "@playwright/test";

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

test("message send round-trips over WS after REST post", async ({ page }) => {
  await login(page);
  await expect(page.locator(".chan", { hasText: "general" })).toBeVisible();
  
  const uniqueText = `e2e-msg-${Math.random().toString(36).slice(2)}`;
  
  const input = page.locator('.composer input');
  await expect(input).toBeVisible();
  await input.fill(uniqueText);
  await input.press("Enter");
  
  const mbody = page.locator(`.mbody`, { hasText: uniqueText });
  await expect(mbody).toBeVisible();
});
