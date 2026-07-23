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

test("non-401 transient errors do not log out user or redirect to login", async ({ page }) => {
  await login(page);

  // Intercept auth/me with 500 error to simulate transient network/server failure
  await page.route("**/api/v1/auth/me", async (route) => {
    await route.fulfill({ status: 500, body: "Server Error" });
  });

  // Reload page
  await page.reload();

  // User remains on shell page (not redirected to /login/)
  expect(page.url()).not.toContain("/login");
});
