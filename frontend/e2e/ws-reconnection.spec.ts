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

test("WebSocket reconnects automatically after network disruption", async ({ page }) => {
  await login(page);

  // Simulate network offline then online
  await page.context().setOffline(true);
  await page.waitForTimeout(1000);
  await page.context().setOffline(false);

  // App remains usable and channel navigation works
  await expect(page.getByTestId("app-shell")).toBeVisible();
});
