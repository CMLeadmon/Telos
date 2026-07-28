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

test("capability-driven navigation and settings visibility", async ({ page }) => {
  await login(page);

  // App shell displays module navigation based on user capabilities
  await expect(page.getByTestId("app-shell")).toBeVisible();

  // Navigate to settings and check visible tabs
  await page.goto("/settings/");
  await expect(page.getByTestId("settings-page")).toBeVisible();

  // Profile, Security, Appearance, Credits are always present
  await expect(page.getByRole("button", { name: "Profile" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Security" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Appearance" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Credits" })).toBeVisible();
});
