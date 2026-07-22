import { test, expect, type Page } from "@playwright/test";

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

test("the notification bell opens a labeled, keyboard-operable inbox", async ({ page }) => {
  await login(page);
  const bell = page.locator('[aria-label="notifications"]');
  await expect(bell).toBeVisible();
  await bell.click();

  const dialog = page.getByRole("dialog", { name: "Notifications" });
  await expect(dialog).toBeVisible();
  await expect(dialog).toHaveAttribute("aria-modal", "true");

  // Escape closes it.
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
});

test("mark-all read clears the unread badge", async ({ page }) => {
  await login(page);
  await page.locator('[aria-label="notifications"]').click();
  const dialog = page.getByRole("dialog", { name: "Notifications" });
  await expect(dialog).toBeVisible();
  await dialog.getByText("Mark all read").click();
  // The badge disappears once the count reaches zero.
  await expect(page.locator(".notif-badge")).toHaveCount(0);
});
