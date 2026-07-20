import { readFileSync } from "node:fs";
import { test, expect, type Page } from "@playwright/test";

// Run this credentialed spec in isolation because the account and catalog are
// shared. E2E_USERNAME should hold manage_library (normally Librarian).
const USERNAME = process.env.E2E_USERNAME;
const PASSWORD = process.env.E2E_PASSWORD;
const MEMBER_USERNAME = process.env.E2E_MEMBER_USERNAME;
const MEMBER_PASSWORD = process.env.E2E_MEMBER_PASSWORD;
const SCRATCH_EPUB = process.env.E2E_SCRATCH_EPUB;
const SCRATCH_TITLE = process.env.E2E_SCRATCH_TITLE;

async function login(page: Page, username: string, password: string) {
  await page.goto("/login/");
  await page.locator("#username").fill(username);
  await page.locator("#password").fill(password);
  await page.getByRole("button", { name: "Enter the node" }).click();
  await page.waitForURL("**/chat/**");
}

async function openPrideSettings(page: Page) {
  await page.goto("/library/");
  const card = page
    .getByTestId("library-card")
    .filter({ hasText: "Pride and Prejudice" });
  await expect(card).toBeVisible({ timeout: 15_000 });
  await card.getByLabel("manage Pride and Prejudice").click();
  return page.getByTestId("book-manage-modal");
}

function editableMetadata(book: Record<string, unknown>) {
  return {
    title: book.title ?? "",
    subtitle: book.subtitle ?? "",
    authors: book.authors ?? [],
    categories: book.categories ?? [],
    language: book.language ?? "",
    description: book.description ?? "",
    seriesName: book.seriesName ?? "",
    seriesNumber: book.seriesNumber ?? null,
    publisher: book.publisher ?? "",
    publishedDate: book.publishedDate ?? "",
    isbn10: book.isbn10 ?? "",
    isbn13: book.isbn13 ?? "",
  };
}

test("management gear is hidden from a plain member", async ({ page }) => {
  test.skip(
    !MEMBER_USERNAME || !MEMBER_PASSWORD,
    "E2E_MEMBER_USERNAME/E2E_MEMBER_PASSWORD not set",
  );
  await login(page, MEMBER_USERNAME!, MEMBER_PASSWORD!);
  await page.goto("/library/");
  await expect(
    page.getByTestId("library-card").filter({ hasText: "Pride and Prejudice" }),
  ).toBeVisible({ timeout: 15_000 });
  await expect(page.getByLabel("manage Pride and Prejudice")).toHaveCount(0);
});

test.describe("librarian book management", () => {
  test.skip(!USERNAME || !PASSWORD, "E2E_USERNAME/E2E_PASSWORD not set");

  test("gear opens settings without opening the reader", async ({ page }) => {
    await login(page, USERNAME!, PASSWORD!);
    const modal = await openPrideSettings(page);
    await expect(modal).toBeVisible();
    await expect(page.getByTestId("book-reader")).toHaveCount(0);
  });

  test("title edit saves to the card and is restored", async ({ page }) => {
    await login(page, USERNAME!, PASSWORD!);
    const originalResponse = await page.request.get("/api/v1/library/books/1");
    expect(originalResponse.ok()).toBeTruthy();
    const original = (await originalResponse.json()) as Record<string, unknown>;
    const originalTitle = String(original.title);
    const temporaryTitle = `${originalTitle} — e2e`;

    try {
      const modal = await openPrideSettings(page);
      const title = modal.getByLabel("Title", { exact: true });
      await expect(title).toHaveValue(originalTitle);
      await title.fill(temporaryTitle);
      await modal.getByRole("button", { name: "Save metadata" }).click();
      await expect(modal).toContainText("Metadata saved.");
      await modal.getByLabel("close book settings").click();
      await expect(
        page.getByTestId("library-card").filter({ hasText: temporaryTitle }),
      ).toBeVisible();
    } finally {
      const restore = await page.request.put("/api/v1/library/books/1/metadata", {
        data: editableMetadata(original),
      });
      expect(restore.ok()).toBeTruthy();
    }
  });

  test("candidate fields apply locally but closing does not persist", async ({
    page,
  }) => {
    await login(page, USERNAME!, PASSWORD!);
    await page.route("**/api/v1/library/books/1/metadata/fetch", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          candidates: [
            {
              provider: "E2E Provider",
              title: "Unsaved candidate title",
              subtitle: "",
              authors: ["Candidate Author"],
              categories: ["Candidate Category"],
              language: "en",
              description: "Candidate description",
              seriesName: "",
              seriesNumber: null,
              publisher: "",
              publishedDate: "",
              isbn10: "",
              isbn13: "",
              coverUrl: "",
            },
          ],
        }),
      });
    });

    let modal = await openPrideSettings(page);
    await modal.getByRole("button", { name: "Fetch metadata" }).click();
    await modal.getByRole("button", { name: /E2E Provider/ }).click();
    await modal.getByRole("button", { name: "Apply selected fields" }).click();
    await expect(modal.getByLabel("Title", { exact: true })).toHaveValue(
      "Unsaved candidate title",
    );
    await modal.getByLabel("close book settings").click();

    modal = await openPrideSettings(page);
    await expect(modal.getByLabel("Title", { exact: true })).toHaveValue(
      "Pride and Prejudice",
    );
  });

  test("uploads and deletes a scratch EPUB", async ({ page }) => {
    test.skip(
      !SCRATCH_EPUB || !SCRATCH_TITLE,
      "E2E_SCRATCH_EPUB/E2E_SCRATCH_TITLE not set",
    );
    expect(SCRATCH_TITLE).not.toBe("Pride and Prejudice");
    await login(page, USERNAME!, PASSWORD!);
    const upload = await page.request.post("/api/v1/files/books", {
      multipart: {
        file: {
          name: `telos-e2e-${Date.now()}.epub`,
          mimeType: "application/epub+zip",
          buffer: readFileSync(SCRATCH_EPUB!),
        },
      },
    });
    expect(upload.ok()).toBeTruthy();

    await page.goto("/library/");
    const card = page
      .getByTestId("library-card")
      .filter({ hasText: SCRATCH_TITLE! });
    await expect(card).toBeVisible({ timeout: 90_000 });
    await card.getByLabel(`manage ${SCRATCH_TITLE}`).click();
    page.once("dialog", (dialog) => dialog.accept());
    await page.getByRole("button", { name: "Delete book" }).click();
    await expect(card).toHaveCount(0, { timeout: 20_000 });
  });
});
