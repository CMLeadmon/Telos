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

test("markdown rendering formats text and sanitizes HTML", async ({ page }) => {
  await login(page);
  await expect(page.locator(".chan", { hasText: "general" })).toBeVisible();

  // Test bold markdown
  const boldText = `boldtext-${Math.random().toString(36).slice(2)}`;
  const boldMarkdown = `e2e-bold-**${boldText}**`;
  const input = page.locator('.composer input');
  await input.fill(boldMarkdown);
  await input.press("Enter");

  const strongElem = page.locator('.mbody strong', { hasText: boldText });
  await expect(strongElem).toBeVisible();

  // Test HTML sanitization/escaping
  const xssPayload = `<img id="xss-test-img" src="x" onerror="console.log('xss')" />`;
  await input.fill(xssPayload);
  await input.press("Enter");

  // Verify the img element is NOT rendered (since rehype-sanitize filters it out)
  const imgElem = page.locator('#xss-test-img');
  await expect(imgElem).not.toBeVisible();
});

test("emoji picker can search and insert emojis", async ({ page }) => {
  await login(page);
  await expect(page.locator(".chan", { hasText: "general" })).toBeVisible();

  // Open picker
  const emojiBtn = page.locator('.composer button[aria-label="emoji"]');
  await emojiBtn.click();

  // Search for emoji
  const searchInput = page.locator('.emoji-picker input[placeholder="Search emoji..."]');
  await expect(searchInput).toBeVisible();
  await searchInput.fill("fire");

  // Pick 🔥
  const fireBtn = page.locator('.emoji-picker button.emoji-btn', { hasText: "🔥" }).first();
  await expect(fireBtn).toBeVisible();
  await fireBtn.click();

  // Verify it is inserted in composer input
  const input = page.locator('.composer input');
  const val = await input.inputValue();
  expect(val).toContain("🔥");

  // Send message
  await input.press("Enter");
  await expect(page.locator('.mbody', { hasText: "🔥" }).last()).toBeVisible();
});

test("@mention autocomplete searches and inserts mentions", async ({ page }) => {
  await login(page);
  await expect(page.locator(".chan", { hasText: "general" })).toBeVisible();

  const input = page.locator('.composer input');
  await input.fill("@");

  // Autocomplete dropdown should appear
  const mentionDropdown = page.locator('.mention-dropdown');
  await expect(mentionDropdown).toBeVisible();

  // Highlight our own test user
  const userBtn = mentionDropdown.locator('button', { hasText: USERNAME! });
  await expect(userBtn).toBeVisible();
  await userBtn.click();

  // Verify input is updated
  const val = await input.inputValue();
  expect(val).toContain(`@${USERNAME!} `);

  // Send message
  await input.press("Enter");
  await expect(page.locator('.mbody', { hasText: `@${USERNAME!}` }).last()).toBeVisible();
});

test("media-share embed loop: share from library, send embed card, and navigate back via read action", async ({ page }) => {
  const catalogRequests: string[] = [];
  page.on("request", (request) => {
    catalogRequests.push(request.url());
  });
  await login(page);

  // Go to Library and share Pride and Prejudice
  await page.goto("/library/");
  const card = page.getByTestId("library-card").filter({ hasText: "Pride and Prejudice" });
  await expect(card).toBeVisible({ timeout: 15000 });

  const booksResponse = await page.request.get("/api/v1/library/books");
  expect(booksResponse.ok()).toBeTruthy();
  const books = (await booksResponse.json()) as Array<{ id: string; title: string }>;
  const sharedBook = books.find((book) => book.title === "Pride and Prejudice");
  expect(sharedBook).toBeTruthy();

  const shareBtn = card.getByRole("link", { name: /^share / });
  await expect(shareBtn).toBeVisible();
  const shareHref = await shareBtn.getAttribute("href");
  expect(new URL(shareHref!, "http://telos.test").searchParams.get("share_ref"))
    .toBe(sharedBook!.id);
  await shareBtn.click();

  // Verify redirected to chat and embed is staged
  await page.waitForURL("**/chat**");
  const stagedPreview = page.locator('span', { hasText: "Pride and Prejudice" });
  await expect(stagedPreview).toBeVisible();

  // Write a message and submit
  const input = page.locator('.composer input');
  await input.fill("Highly recommended read!");
  await input.press("Enter");

  // Verify message and rich card are rendered
  await expect(page.locator('.mbody', { hasText: "Highly recommended read!" }).last()).toBeVisible();
  const embedCard = page.locator('.share-card', { hasText: "Pride and Prejudice" }).last();
  await expect(embedCard).toBeVisible();
  await expect(embedCard.locator('div', { hasText: "LIBRARY BOOK" }).first()).toBeVisible();

  // Click read button inside the card
  const readActionBtn = embedCard.locator('button', { hasText: "Read" });
  await expect(readActionBtn).toBeVisible();
  await readActionBtn.click();

  // Verify it navigates back to library and opens book reader
  await page.waitForURL("**/library/**");
  const reader = page.getByTestId("book-reader");
  await expect(reader).toBeVisible({ timeout: 30000 });
  await expect(reader).toContainText("Pride and Prejudice");
  expect(catalogRequests.join(" ")).not.toMatch(
    /grimmory|jellyfin|api[_-]?key|access[_-]?token/i,
  );
});
