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

async function openPicker(page: Page, option: RegExp) {
  await page.locator('.composer .iconbtn[aria-label="attach"]').click();
  await page.locator(".attach-opt-btn", { hasText: option }).click();
  await expect(page.getByTestId("share-picker-list")).toBeVisible();
}

const folders = (page: Page) => page.getByTestId("share-picker-folder");
const leaves = (page: Page) => page.getByTestId("share-picker-leaf");

// Walks into the first folder repeatedly until something shareable appears.
// Content-agnostic on purpose: what matters is that depth is reachable at all,
// not which show happens to be on the node.
async function drillToLeaf(page: Page, maxDepth = 5): Promise<string> {
  for (let depth = 0; depth < maxDepth; depth++) {
    await expect(leaves(page).or(folders(page)).first()).toBeVisible();
    if (await leaves(page).count()) {
      return (await leaves(page).first().locator(".sp-name").innerText()).trim();
    }
    await folders(page).first().click();
  }
  throw new Error(`no shareable item within ${maxDepth} levels`);
}

// Nothing here sends a message. Staging and dismissing exercises the whole
// pick path without writing to chat history.
async function dismissStaged(page: Page) {
  const staged = page.locator("div", { hasText: /^Staged Embed:/ }).last();
  await staged.locator("button").click();
  await expect(page.getByText("Staged Embed:")).toHaveCount(0);
}

test("the picker reaches media nested below the top level", async ({ page }) => {
  await login(page);
  await openPicker(page, /Share from Stream/);

  // The old picker listed only direct children of each library and dropped
  // every folder, so a node whose media is nested offered nothing at all.
  const title = await drillToLeaf(page);
  expect(title.length).toBeGreaterThan(0);

  await expect(page.getByTestId("share-picker-crumbs")).toBeVisible();

  await leaves(page).first().click();
  await expect(page.getByText("Staged Embed:")).toBeVisible();
  await expect(page.getByText(title, { exact: false }).last()).toBeVisible();
  await dismissStaged(page);
});

test("a breadcrumb climbs back out of the media trail", async ({ page }) => {
  await login(page);
  await openPicker(page, /Share from Stream/);

  await expect(folders(page).first()).toBeVisible();
  const topLevel = await folders(page).first().locator(".sp-name").innerText();

  await folders(page).first().click();
  await expect(page.getByTestId("share-picker-crumbs")).toContainText("Stream");

  await page.getByTestId("share-picker-crumbs").getByRole("button", { name: "Stream" }).click();
  await expect(folders(page).first().locator(".sp-name")).toHaveText(topLevel);

  await page.keyboard.press("Escape");
  await expect(page.getByTestId("share-picker-list")).toHaveCount(0);
});

test("files are shareable from the picker", async ({ page }) => {
  await login(page);
  await openPicker(page, /Share a file/);

  // The gateway and ShareCard have always supported the file kind; the picker
  // simply had no Files tab, so it was unreachable.
  const title = await drillToLeaf(page);
  expect(title.length).toBeGreaterThan(0);

  await leaves(page).first().click();
  await expect(page.getByText("Staged Embed:")).toBeVisible();
  await expect(page.getByText("(File)")).toBeVisible();
  await dismissStaged(page);
});

test("search reaches content without walking the tree", async ({ page }) => {
  await login(page);
  await openPicker(page, /Share from Stream/);

  const title = await drillToLeaf(page);
  // Back to the top, then find the same item by name instead of by walking.
  await page.getByTestId("share-picker-crumbs").getByRole("button", { name: "Stream" }).click();

  const term = title.slice(0, Math.min(6, title.length));
  await page.getByLabel("search shareable content").fill(term);

  await expect(page.getByTestId("share-picker-list")).toContainText(title);
  await expect(page.getByTestId("share-picker-crumbs")).toHaveCount(0);

  await page.keyboard.press("Escape");
});

test("the files browser offers a share action per file", async ({ page }) => {
  await login(page);
  await page.goto("/files/");

  // The shelf is folders-first, and a file may be several levels down. Walk
  // into the first folder until one turns up.
  const shareLink = page.getByRole("link", { name: /^share .+ to chat$/ }).first();
  const fileRows = page.getByTestId("file-row");
  const folderRows = page.getByTestId("folder-row");
  await expect(page.getByTestId("files-table")).toBeVisible();
  for (let depth = 0; depth < 4; depth++) {
    if (await fileRows.count()) break;
    const name = (await folderRows.first().locator(".fname").innerText()).trim();
    await folderRows.first().click();
    // The table re-renders on navigation; wait for it to land before probing.
    await expect(page.getByTestId("files-breadcrumbs")).toContainText(name);
    await expect(page.getByTestId("files-table")).toBeVisible();
  }
  await expect(shareLink).toBeVisible();

  await shareLink.click();
  await page.waitForURL("**/chat**");
  await expect(page.getByText("Staged Embed:")).toBeVisible();
  await expect(page.getByText("(File)")).toBeVisible();
  await dismissStaged(page);
});
