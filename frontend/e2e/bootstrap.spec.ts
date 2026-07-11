import { test, expect } from "@playwright/test";

// Full first-boot verification against a live gateway. Consumes the one-time
// bootstrap (creates the Owner), so it only runs when explicitly armed:
//   E2E_BOOTSTRAP_TOKEN=<token> E2E_BASE_URL=http://localhost:8080 npx playwright test bootstrap
// Clean up the created owner afterwards if a human still needs to bootstrap.
const TOKEN = process.env.E2E_BOOTSTRAP_TOKEN;
const USERNAME = process.env.E2E_BOOTSTRAP_USERNAME ?? "telos-e2e-owner";
const PASSWORD = process.env.E2E_BOOTSTRAP_PASSWORD ?? "e2e-owner-pass-0123456789";

test.skip(!TOKEN, "E2E_BOOTSTRAP_TOKEN not set — skipping one-shot bootstrap test");

test("first boot creates the Owner and lands in the app shell", async ({
  page,
}) => {
  await page.goto("/login/");
  await page.getByRole("button", { name: "first boot" }).click();

  await page.locator("#username").fill(USERNAME);
  await page.locator("#password").fill(PASSWORD);
  await page.locator("#token").fill(TOKEN!);
  await page.getByRole("button", { name: "Bootstrap owner" }).click();

  await page.waitForURL("**/chat/**");
  await expect(page.getByTestId("app-shell")).toBeVisible();
  // Session chip shows the logged-in owner
  await expect(page.locator(".topacts .chip")).toContainText(USERNAME);
  // Channel rail loaded from GET /api/v1/channels (seeded channels)
  await expect(page.locator(".chan", { hasText: "general" })).toBeVisible();
});
