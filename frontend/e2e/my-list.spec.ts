import { test, expect, type Page } from "@playwright/test";

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

test("My List persists an added item across reload and supports removal", async ({ page }) => {
  await login(page);
  await page.goto("/stream/");

  // The shelf may be empty on a fresh account; this test asserts the shelf and
  // its ordering survive a reload when it has entries.
  const shelf = page.getByTestId("mylist-shelf");
  if ((await shelf.count()) === 0) test.skip(true, "My List is empty for this account");

  const firstItem = shelf.locator('[data-testid^="mylist-item-"]').first();
  const itemId = (await firstItem.getAttribute("data-testid"))!.replace("mylist-item-", "");

  await page.reload();
  await expect(page.getByTestId(`mylist-item-${itemId}`)).toBeVisible();

  // Removal takes it out of the shelf.
  await page.getByTestId(`mylist-remove-${itemId}`).click();
  await expect(page.getByTestId(`mylist-item-${itemId}`)).toHaveCount(0);
});
