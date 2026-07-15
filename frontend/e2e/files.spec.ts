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

test("folders: create, enter, upload inside, scope, delete", async ({ page }) => {
  await login(page);
  await page.goto("/files/");
  await expect(page.getByTestId("files-dropzone")).toBeVisible();

  const folder = `e2e-dir-${Math.random().toString(36).slice(2, 8)}`;
  const fileName = `inside-${Math.random().toString(36).slice(2, 8)}.png`;

  // Create a folder.
  await page.getByTestId("new-folder-btn").click();
  await page.getByTestId("new-folder-input").fill(folder);
  await page.getByTestId("new-folder-input").press("Enter");
  const folderRow = page
    .getByTestId("folder-row")
    .filter({ hasText: folder })
    .first();
  await expect(folderRow).toBeVisible({ timeout: 10_000 });

  // Enter it and upload a file inside.
  await folderRow.click();
  await expect(page.getByTestId("files-breadcrumbs")).toContainText(folder);
  await page.getByTestId("files-input").setInputFiles({
    name: fileName,
    mimeType: "image/png",
    buffer: PNG,
  });
  const fileRow = page.getByTestId("file-row").filter({ hasText: fileName });
  await expect(fileRow).toBeVisible({ timeout: 30_000 }); // upload + scan

  // Scope: the file is NOT visible back at root.
  await page.getByRole("button", { name: "Home" }).click();
  await expect(page.getByTestId("file-row").filter({ hasText: fileName })).toHaveCount(0);

  // Deleting the non-empty folder is refused with a notice.
  const rootFolderRow = page
    .getByTestId("folder-row")
    .filter({ hasText: folder })
    .first();
  await rootFolderRow.getByLabel(`delete folder ${folder}`).click();
  await rootFolderRow.getByLabel(`confirm delete ${folder}`).click();
  await expect(page.locator(".fnotice")).toContainText("isn't empty");

  // Empty it (enter, delete the file), then the folder deletes cleanly.
  await rootFolderRow.click();
  await expect(page.getByTestId("files-breadcrumbs")).toContainText(folder);
  const innerRow = page.getByTestId("file-row").filter({ hasText: fileName });
  await innerRow.getByLabel(`delete ${fileName}`).click();
  await innerRow.getByLabel(`confirm delete ${fileName}`).click();
  await expect(page.getByTestId("file-row").filter({ hasText: fileName })).toHaveCount(0, {
    timeout: 15_000,
  });

  await page.getByRole("button", { name: "Home" }).click();
  const finalRow = page.getByTestId("folder-row").filter({ hasText: folder }).first();
  await finalRow.getByLabel(`delete folder ${folder}`).click();
  await finalRow.getByLabel(`confirm delete ${folder}`).click();
  await expect(page.getByTestId("folder-row").filter({ hasText: folder })).toHaveCount(0, {
    timeout: 10_000,
  });
});
