import { test, expect } from "@playwright/test";

test("accessibility smoke tests for landing and login pages", async ({ page }) => {
  await page.goto("/");
  await expect(page.locator("body")).toBeVisible();

  await page.goto("/login/");
  await expect(page.locator("body")).toBeVisible();
  await expect(page.locator("#username")).toBeVisible();
  await expect(page.locator("#password")).toBeVisible();
});
