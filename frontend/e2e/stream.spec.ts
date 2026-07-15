import { test, expect, type Page } from "@playwright/test";

// Credentialed run against a live gateway with real Jellyfin libraries:
//   E2E_USERNAME=<user> E2E_PASSWORD=<pass> npx playwright test stream
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

test("drilling into a folder poster shows breadcrumbs and its children", async ({
  page,
}) => {
  await login(page);
  await page.goto("/stream/");
  await expect(page.getByTestId("stream-browse")).toBeVisible({
    timeout: 15_000,
  });

  const folder = page.getByTestId("poster-folder").first();
  test.skip(
    (await folder.count()) === 0,
    "no folder-type media (series/audiobook) on this node to drill into",
  );

  const title = (await folder.locator(".pt").textContent())!.trim();
  await folder.click();

  const crumbs = page.getByTestId("stream-crumbs");
  await expect(crumbs).toBeVisible();
  await expect(crumbs).toContainText(title);

  // Home breadcrumb clears the drill and returns to the top-level rows.
  await page.getByRole("button", { name: "Home" }).click();
  await expect(page.getByTestId("stream-crumbs")).toHaveCount(0);
});

test("drilling down to a leaf item starts playback", async ({ page }) => {
  await login(page);
  await page.goto("/stream/");
  await expect(page.getByTestId("stream-browse")).toBeVisible({
    timeout: 15_000,
  });

  // Descend through folders (series -> season -> episode, or book ->
  // chapter) until a leaf poster appears, then play it.
  let leafFound = false;
  for (let i = 0; i < 5 && !leafFound; i++) {
    const leaf = page.getByTestId("poster-leaf").first();
    if (await leaf.count()) {
      await leaf.click();
      leafFound = true;
      break;
    }
    const folder = page.getByTestId("poster-folder").first();
    test.skip((await folder.count()) === 0, "no playable media on this node");
    await folder.click();
    await expect(page.getByTestId("stream-crumbs")).toBeVisible();
  }
  test.skip(!leafFound, "no leaf item reachable within 5 levels");

  await expect(page.getByTestId("stream-player")).toBeVisible({
    timeout: 10_000,
  });
  await expect(page.getByTestId("stream-video")).toBeVisible();
});
