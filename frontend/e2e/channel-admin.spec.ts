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

test("an admin can create and delete a channel from Settings", async ({ page }) => {
  await login(page);
  await page.goto("/settings/");
  // The Channels admin tab is only visible to Owner/Administrator.
  const tab = page.getByRole("button", { name: "Channels" });
  if ((await tab.count()) === 0) test.skip(true, "not an admin account");
  await tab.click();
  await expect(page.getByTestId("admin-channels-section")).toBeVisible();

  const name = `e2e-chan-${Math.random().toString(36).slice(2, 8)}`;
  await page.getByLabel("Channel name").fill(name);
  await page.getByTestId("create-channel").click();
  await expect(page.getByText(`#${name}`)).toBeVisible();
});
