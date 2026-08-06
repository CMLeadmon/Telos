import { test, expect, type Page } from "@playwright/test";

// Credentialed run against a live gateway:
//   E2E_USERNAME=<user> E2E_PASSWORD=<pass> npx playwright test theme
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

const currentTheme = (page: Page) =>
  page.evaluate(() => document.documentElement.dataset.theme);

// The regression this suite was missing. Preference rows written before the
// voice feature was removed still carried keys like voiceInputGain; the client
// echoed them back and the gateway rejected the whole PUT with 400 — silently.
// The theme therefore reverted on reload, because load() lets the unchanged
// server value win.
test("a theme change survives a reload", async ({ page }) => {
  await login(page);
  const before = await currentTheme(page);
  expect(before).toBeTruthy();

  await page.getByRole("button", { name: "toggle theme" }).click();
  await expect
    .poll(() => currentTheme(page), { timeout: 10_000 })
    .not.toBe(before);
  const after = await currentTheme(page);

  await page.reload({ waitUntil: "networkidle" });
  await expect.poll(() => currentTheme(page), { timeout: 10_000 }).toBe(after);

  // Leave the account as we found it.
  await page.getByRole("button", { name: "toggle theme" }).click();
  await expect.poll(() => currentTheme(page), { timeout: 10_000 }).toBe(before);
});

test("the preference save is accepted by the gateway", async ({ page }) => {
  await login(page);

  const put = page.waitForResponse(
    (r) =>
      r.url().includes("/api/v1/users/me/preferences") &&
      r.request().method() === "PUT",
  );
  await page.getByRole("button", { name: "toggle theme" }).click();
  const res = await put;

  expect(res.status()).toBe(200);
  // Only keys the gateway's allow-list accepts may be sent.
  const sent = JSON.parse(res.request().postData() ?? "{}");
  expect(Object.keys(sent).sort()).toEqual([
    "reducedMotion",
    "sceneEnabled",
    "theme",
  ]);

  await page.getByRole("button", { name: "toggle theme" }).click();
});

test("the theme cookie tracks the active theme", async ({ page, context }) => {
  await login(page);
  const active = await currentTheme(page);

  const themeCookie = async () =>
    (await context.cookies()).find((c) => c.name === "telos_theme")?.value;

  await expect.poll(themeCookie, { timeout: 10_000 }).toBe(active);

  await page.getByRole("button", { name: "toggle theme" }).click();
  await expect.poll(() => currentTheme(page), { timeout: 10_000 }).not.toBe(active);
  const flipped = await currentTheme(page);
  await expect.poll(themeCookie, { timeout: 10_000 }).toBe(flipped);

  await page.getByRole("button", { name: "toggle theme" }).click();
});
