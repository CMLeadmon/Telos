import { test, expect, type Page } from "@playwright/test";

// Credentialed run against a live gateway with real Jellyfin libraries:
//   E2E_USERNAME=<user> E2E_PASSWORD=<pass> npx playwright test stream
const USERNAME = process.env.E2E_USERNAME;
const PASSWORD = process.env.E2E_PASSWORD;
const CANONICAL_MEDIA_ID = "11111111-1111-4111-8111-111111111112";
const LEGACY_MEDIA_ID = "legacy-film";
const OPAQUE_MEDIA_ID = "catalog/film ?part=1";
const liveTest = USERNAME && PASSWORD ? test : test.skip;

async function login(page: Page) {
  await page.goto("/login/");
  await page.locator("#username").fill(USERNAME!);
  await page.locator("#password").fill(PASSWORD!);
  await page.getByRole("button", { name: "Enter the node" }).click();
  await page.waitForURL("**/chat/**");
}

async function mockStreamContinuity(page: Page) {
  const requestedUrls: string[] = [];
  page.on("request", (request) => requestedUrls.push(request.url()));

  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());

    if (url.pathname === "/api/v1/auth/me") {
      await route.fulfill({
        json: {
          ID: "member-1",
          Username: "viewer",
          DisplayName: "Viewer",
          HasAvatar: false,
          Roles: ["Member"],
        },
      });
      return;
    }
    if (url.pathname === "/api/v1/users/me/preferences") {
      await route.fulfill({ json: {} });
      return;
    }
    if (url.pathname === "/api/v1/channels") {
      await route.fulfill({ json: [] });
      return;
    }
    if (url.pathname === "/api/v1/media") {
      await route.fulfill({ json: [] });
      return;
    }
    if (
      url.pathname === `/api/v1/media/items/${CANONICAL_MEDIA_ID}` ||
      url.pathname === `/api/v1/media/items/${LEGACY_MEDIA_ID}` ||
      url.pathname === `/api/v1/media/items/${encodeURIComponent(OPAQUE_MEDIA_ID)}`
    ) {
      await route.fulfill({
        json: {
          id: CANONICAL_MEDIA_ID,
          title: "Canonical Film",
          durationSec: 5400,
          kind: "video",
        },
      });
      return;
    }
    if (
      url.pathname ===
      `/api/v1/media/items/${CANONICAL_MEDIA_ID}/comments`
    ) {
      await route.fulfill({ json: { annotations: [] } });
      return;
    }
    if (
      url.pathname === `/api/v1/stream/video/${CANONICAL_MEDIA_ID}` ||
      url.pathname.startsWith(
        `/api/v1/stream/video/${CANONICAL_MEDIA_ID}/`,
      )
    ) {
      await route.fulfill({
        status: 200,
        contentType: "application/vnd.apple.mpegurl",
        body: "#EXTM3U\n#EXT-X-ENDLIST\n",
      });
      return;
    }

    await route.fulfill({ status: 404, body: "not found" });
  });

  return requestedUrls;
}

test("canonical, legacy, and opaque Stream links play the same canonical item", async ({
  page,
}) => {
  const requestedUrls = await mockStreamContinuity(page);

  for (const id of [CANONICAL_MEDIA_ID, LEGACY_MEDIA_ID, OPAQUE_MEDIA_ID]) {
    await page.goto(`/stream/?play=${encodeURIComponent(id)}`);
    await expect(page.getByTestId("stream-player")).toContainText(
      "Canonical Film",
    );
    await expect(page.getByTestId("commentary-panel")).toBeVisible();
  }

  expect(
    requestedUrls.some(
      (raw) =>
        new URL(raw).pathname ===
        `/api/v1/media/items/${encodeURIComponent(OPAQUE_MEDIA_ID)}`,
    ),
  ).toBe(true);
  expect(
    requestedUrls.some(
      (raw) =>
        new URL(raw).pathname === `/api/v1/media/items/${LEGACY_MEDIA_ID}`,
    ),
  ).toBe(true);
  const appOrigin = new URL(page.url()).origin;
  expect(
    requestedUrls
      .filter((raw) => new URL(raw).pathname.startsWith("/api/v1/"))
      .every((raw) => new URL(raw).origin === appOrigin),
  ).toBe(true);
  expect(requestedUrls.join(" ")).not.toMatch(
    /jellyfin|grimmory|api[_-]?key|access[_-]?token/i,
  );
});

liveTest("drilling into a folder poster shows breadcrumbs and its children", async ({
  page,
}) => {
  await login(page);
  await page.goto("/stream/");
  await expect(page.getByTestId("stream-browse")).toBeVisible({
    timeout: 15_000,
  });
  // Libraries render immediately; each row's items resolve asynchronously
  // afterward, so give any poster (folder or leaf) time to appear before
  // deciding whether a folder exists.
  await page
    .locator('[data-testid="poster-folder"], [data-testid="poster-leaf"]')
    .first()
    .waitFor({ timeout: 15_000 })
    .catch(() => {});

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

liveTest("drilling down to a leaf item starts playback", async ({ page }) => {
  await login(page);
  await page.goto("/stream/");
  await expect(page.getByTestId("stream-browse")).toBeVisible({
    timeout: 15_000,
  });
  await page
    .locator('[data-testid="poster-folder"], [data-testid="poster-leaf"]')
    .first()
    .waitFor({ timeout: 15_000 })
    .catch(() => {});

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

  // A leaf opens its detail surface first; playback is the primary action
  // there, not a side effect of the click.
  const detail = page.getByTestId("stream-item-detail");
  await expect(detail).toBeVisible({ timeout: 15_000 });
  await detail.getByRole("button", { name: /^(Play|Resume)$/ }).click();

  await expect(page.getByTestId("stream-player")).toBeVisible({
    timeout: 10_000,
  });
  await expect(page.getByTestId("stream-video")).toBeVisible();
});
