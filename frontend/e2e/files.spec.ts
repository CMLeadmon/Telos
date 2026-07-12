import { test, expect, type Page } from "@playwright/test";

// Credentialed run against a live gateway:
//   E2E_USERNAME=<user> E2E_PASSWORD=<pass> npx playwright test files
const USERNAME = process.env.E2E_USERNAME;
const PASSWORD = process.env.E2E_PASSWORD;

test.skip(!USERNAME || !PASSWORD, "E2E_USERNAME/E2E_PASSWORD not set");

// Tiny valid PNG (1x1 transparent) — passes the gateway's magic-byte sniff.
const PNG = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==",
  "base64",
);

async function login(page: Page) {
  await page.goto("/login/");
  await page.locator("#username").fill(USERNAME!);
  await page.locator("#password").fill(PASSWORD!);
  await page.getByRole("button", { name: "Enter the node" }).click();
  await page.waitForURL("**/chat/**");
}

test("files module lists, uploads and deletes", async ({ page }) => {
  await login(page);
  await page.goto("/files/");
  await expect(page.getByTestId("files-dropzone")).toBeVisible();

  const name = `e2e-${Math.random().toString(36).slice(2)}.png`;
  await page.getByTestId("files-input").setInputFiles({
    name,
    mimeType: "image/png",
    buffer: PNG,
  });

  const row = page.getByTestId("file-row").filter({ hasText: name }).first();
  await expect(row).toBeVisible({ timeout: 30_000 }); // upload + ClamAV scan

  await expect(row.locator(".scanpill")).toHaveText("clean");

  await row.getByLabel(`delete ${name}`).click();
  await row.getByLabel(`confirm delete ${name}`).click();
  await expect(
    page.getByTestId("file-row").filter({ hasText: name }),
  ).toHaveCount(0, { timeout: 15_000 });
});
