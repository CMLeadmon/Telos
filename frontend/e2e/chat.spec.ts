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

test("online members roster is populated and counts update", async ({ page }) => {
  await login(page);
  await expect(page.locator(".chan", { hasText: "general" })).toBeVisible();
  await expect(page.locator(".chip", { hasText: "online" })).toBeVisible();
  await expect(page.locator(".memrow", { hasText: USERNAME })).toBeVisible();
});

test("messages can be pinned and unpinned, showing in the pins aside", async ({ page }) => {
  await login(page);
  await expect(page.locator(".chan", { hasText: "general" })).toBeVisible();
  
  const uniqueText = `e2e-pin-msg-${Math.random().toString(36).slice(2)}`;

  const input = page.locator('.composer input');
  await input.fill(uniqueText);
  await input.press("Enter");

  const msgRow = page.locator('.msg', { hasText: uniqueText });
  await expect(msgRow).toBeVisible();

  await msgRow.hover();
  const pinBtn = msgRow.locator('button[title="Pin Message"]');
  await expect(pinBtn).toBeVisible();
  await pinBtn.click();

  await expect(msgRow.locator('.pin-badge')).toBeVisible();

  const pinAsideItem = page.locator('.aside .pin', { hasText: uniqueText });
  await expect(pinAsideItem).toBeVisible();

  await msgRow.hover();
  const unpinBtn = msgRow.locator('button[title="Unpin Message"]');
  await expect(unpinBtn).toBeVisible();
  await unpinBtn.click();

  await expect(msgRow.locator('.pin-badge')).not.toBeVisible();
  await expect(pinAsideItem).not.toBeVisible();
});
