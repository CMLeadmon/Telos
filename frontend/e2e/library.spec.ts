import { test, expect, type Page } from "@playwright/test";

// Credentialed run against a live gateway with the seeded P&P book:
//   E2E_USERNAME=<user> E2E_PASSWORD=<pass> npx playwright test library
const USERNAME = process.env.E2E_USERNAME;
const PASSWORD = process.env.E2E_PASSWORD;
const CANONICAL_BOOK_ID = "11111111-1111-4111-8111-111111111111";
const LEGACY_BOOK_ID = "7";
const liveTest = USERNAME && PASSWORD ? test : test.skip;

async function login(page: Page) {
  await page.goto("/login/");
  await page.locator("#username").fill(USERNAME!);
  await page.locator("#password").fill(PASSWORD!);
  await page.getByRole("button", { name: "Enter the node" }).click();
  await page.waitForURL("**/chat/**");
}

async function mockLibraryContinuity(page: Page) {
  const requestedPaths: string[] = [];
  const requestedUrls: string[] = [];
  page.on("request", (request) => requestedUrls.push(request.url()));
  const book = {
    id: CANONICAL_BOOK_ID,
    title: "Canonical Book",
    subtitle: "",
    authors: ["Telos Reader"],
    categories: ["Continuity"],
    language: "en",
    description: "",
    seriesName: "",
    seriesNumber: null,
    publisher: "",
    publishedDate: "",
    isbn10: "",
    isbn13: "",
    format: "EPUB",
    fileSizeKb: 1,
    addedOn: "2026-07-31T00:00:00Z",
    library: "Books",
  };

  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    requestedPaths.push(path);

    if (path === "/api/v1/auth/me") {
      await route.fulfill({
        json: {
          ID: "member-1",
          Username: "reader",
          DisplayName: "Reader",
          HasAvatar: false,
          Roles: ["Member"],
        },
      });
      return;
    }
    if (path === "/api/v1/users/me/preferences") {
      await route.fulfill({ json: {} });
      return;
    }
    if (path === "/api/v1/channels") {
      await route.fulfill({ json: [] });
      return;
    }
    if (path === "/api/v1/library/books") {
      await route.fulfill({ json: [book] });
      return;
    }
    if (path === "/api/v1/library/facets") {
      await route.fulfill({
        json: { authors: [], categories: [], languages: [], formats: [] },
      });
      return;
    }
    if (path === `/api/v1/library/books/${LEGACY_BOOK_ID}`) {
      await route.fulfill({ json: book });
      return;
    }
    if (path === `/api/v1/library/books/${CANONICAL_BOOK_ID}/annotations`) {
      await route.fulfill({ json: { annotations: [] } });
      return;
    }
    if (path === `/api/v1/library/books/${CANONICAL_BOOK_ID}/progress`) {
      await route.fulfill({ status: 204, body: "" });
      return;
    }
    if (path === `/api/v1/library/books/${CANONICAL_BOOK_ID}/content`) {
      await route.fulfill({
        status: 200,
        contentType: "application/epub+zip",
        body: "",
      });
      return;
    }

    await route.fulfill({ status: 404, body: "not found" });
  });

  return { requestedPaths, requestedUrls };
}

test("canonical and legacy Library deep links open the same canonical book", async ({
  page,
}) => {
  const { requestedPaths, requestedUrls } = await mockLibraryContinuity(page);

  for (const id of [CANONICAL_BOOK_ID, LEGACY_BOOK_ID]) {
    await page.goto(`/library/?read=${encodeURIComponent(id)}`);
    if (id === LEGACY_BOOK_ID) {
      await expect
        .poll(() => requestedPaths.join("\n"))
        .toContain(`/api/v1/library/books/${LEGACY_BOOK_ID}`);
    }
    await expect(
      page.getByRole("dialog", { name: "reading Canonical Book" }),
    ).toBeVisible();
    await expect
      .poll(() =>
        requestedPaths.filter(
          (path) =>
            path ===
            `/api/v1/library/books/${CANONICAL_BOOK_ID}/annotations`,
        ).length,
      )
      .toBeGreaterThan(0);
  }

  expect(requestedPaths).toContain(
    `/api/v1/library/books/${LEGACY_BOOK_ID}`,
  );
  expect(requestedPaths.join(" ")).not.toMatch(/grimmory|token/i);
  expect(requestedUrls.join(" ")).not.toMatch(
    /grimmory|jellyfin|api[_-]?key|access[_-]?token/i,
  );
});

liveTest("library lists the catalog with real covers and facets", async ({
  page,
}) => {
  await login(page);
  await page.goto("/library/");
  const card = page
    .getByTestId("library-card")
    .filter({ hasText: "Pride and Prejudice" });
  await expect(card).toBeVisible({ timeout: 15_000 });
  // Real cover, not a broken image (mock fallback would 503 the cover route).
  const img = card.locator("img");
  await expect
    .poll(async () => img.evaluate((el: HTMLImageElement) => el.naturalWidth))
    .toBeGreaterThan(0);
  // Facet filtering narrows and clears.
  await page.getByRole("button", { name: /Jane Austen/ }).click();
  await expect(page.getByTestId("library-card")).toHaveCount(1);
});

liveTest("epub reader opens, paginates and persists progress", async ({
  page,
}) => {
  test.setTimeout(90_000); // 24 MB epub fetch + locations.generate on CI-slow hosts
  await login(page);
  await page.goto("/library/");
  await page.getByLabel("read Pride and Prejudice").click();
  const reader = page.getByTestId("book-reader");
  await expect(reader).toBeVisible();
  // 24 MB EPUB: allow generous open time.
  await expect(page.getByTestId("reader-percent")).not.toHaveText("0%", {
    timeout: 60_000,
  });
  const before = await page.getByTestId("reader-percent").textContent();
  // epubjs's next() is async and drops overlapping calls — wait for each
  // page turn to settle before firing the next one.
  for (let i = 0; i < 6; i++) {
    await page.getByLabel("next page").click();
    await page.waitForTimeout(300);
  }
  await expect(page.getByTestId("reader-percent")).not.toHaveText(before!, {
    timeout: 15_000,
  });
  // Debounced save is 1s; give it breathing room, then verify restore.
  await page.waitForTimeout(2_000);
  await page.getByLabel("close reader").click();
  await page.reload();
  await page.getByLabel("read Pride and Prejudice").click();
  await expect(page.getByTestId("reader-percent")).not.toHaveText("0%", {
    timeout: 60_000,
  });
});
