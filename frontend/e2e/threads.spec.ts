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

test("opening a thread displays the root message, replies, and allows sending a reply", async ({ page }) => {
  await login(page);
  await expect(page.locator(".chan", { hasText: "general" })).toBeVisible();

  // Post a root message
  const rootText = `e2e-thread-root-${Math.random().toString(36).slice(2)}`;
  const mainInput = page.locator(".composer input");
  await mainInput.fill(rootText);
  await mainInput.press("Enter");

  const rootMsg = page.locator(".msg", { hasText: rootText });
  await expect(rootMsg).toBeVisible();

  // Hover and click "Reply in thread"
  await rootMsg.hover();
  const replyBtn = rootMsg.locator('button[title="Reply in thread"]');
  await expect(replyBtn).toBeVisible();
  await replyBtn.click();

  // Verify Thread Panel opens
  const threadPanel = page.getByTestId("thread-panel");
  await expect(threadPanel).toBeVisible();
  await expect(page.getByTestId("thread-root")).toContainText(rootText);

  // Send a reply in the thread
  const replyText = `e2e-reply-${Math.random().toString(36).slice(2)}`;
  const replyInput = page.getByTestId("thread-reply-input");
  await replyInput.fill(replyText);
  await replyInput.press("Enter");

  // Reply should clear input and appear in the thread replies list
  await expect(replyInput).toHaveValue("");
  await expect(threadPanel.locator(".thread-reply", { hasText: replyText })).toBeVisible();

  // Close thread
  await page.locator(".thread-close").click();
  await expect(threadPanel).not.toBeVisible();
});
