import { test, expect } from "@playwright/test";

test("landing renders the synthwave hero", async ({ page }) => {
  await page.goto("/");
  await expect(page.locator(".hero h1")).toContainText("discuss");
  await expect(page.locator(".slog")).toContainText(
    "be on the net, but not of the net",
  );
});

test("login page shows the auth card and mode switching", async ({ page }) => {
  await page.goto("/login/");
  await expect(page.getByTestId("auth-page")).toBeVisible();
  await expect(page.locator("#username")).toBeVisible();
  await page.getByRole("button", { name: "first boot" }).click();
  await expect(page.locator("#token")).toBeVisible();
});

test("shell routes redirect anonymous users to login", async ({ page }) => {
  await page.goto("/chat/");
  await page.waitForURL("**/login/**");
  await expect(page.getByTestId("auth-page")).toBeVisible();
});
