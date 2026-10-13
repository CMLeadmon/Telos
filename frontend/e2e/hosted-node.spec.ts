import { test, expect, type Page } from "@playwright/test";

// Hosted-node journeys: real browser, real HTTPS origin, NO route mocking.
//
//   E2E_BASE_URL=https://community.example.net \
//   E2E_OWNER_USERNAME=... E2E_OWNER_PASSWORD=... \
//   E2E_MEMBER_USERNAME=... E2E_MEMBER_PASSWORD=... \
//   E2E_REQUIRE_LIVE=1 npx playwright test hosted-node
//
// With E2E_REQUIRE_LIVE=1 a missing setting FAILS the run instead of skipping it,
// so a release rehearsal can demand zero skips. Without it (local development) the
// file skips cleanly. Credentials come from the environment of the tester's host
// and are never logged or attached; screenshots/traces stay off for these tests.
const BASE = process.env.E2E_BASE_URL ?? "";
const OWNER = { user: process.env.E2E_OWNER_USERNAME, pass: process.env.E2E_OWNER_PASSWORD };
const MEMBER = { user: process.env.E2E_MEMBER_USERNAME, pass: process.env.E2E_MEMBER_PASSWORD };
const REQUIRE_LIVE = process.env.E2E_REQUIRE_LIVE === "1";
const CONFIGURED = BASE.startsWith("https://") && !!(OWNER.user && OWNER.pass && MEMBER.user && MEMBER.pass);

test.use({ trace: "off", video: "off", screenshot: "off" });

test.describe("hosted node (cookie mode, same origin)", () => {
  test.beforeEach(() => {
    if (REQUIRE_LIVE) {
      expect(
        CONFIGURED,
        "E2E_REQUIRE_LIVE=1 needs an https E2E_BASE_URL plus owner and member credentials",
      ).toBe(true);
    } else {
      test.skip(!CONFIGURED, "hosted-node settings are not configured");
    }
  });

  async function signIn(page: Page, who: { user?: string; pass?: string }) {
    await page.goto("/login/");
    await page.waitForLoadState("networkidle");
    await page.locator("#username").fill(who.user!);
    await page.locator("#password").fill(who.pass!);
    await page.getByRole("button", { name: "Enter the node" }).click();
    await page.waitForURL("**/chat/**");
  }

  test("the origin is HTTPS and the page comes from the static client", async ({ page }) => {
    expect(new URL(BASE).protocol).toBe("https:");
    const response = await page.goto("/");
    expect(response?.status()).toBe(200);
    expect(response?.headers()["content-type"]).toContain("text/html");
    const policy = response?.headers()["content-security-policy"] ?? "";
    expect(policy).toContain("default-src 'self'");
    expect(policy).toContain(`wss://${new URL(BASE).host}`);
    // A mistyped API path is the gateway's 404, never the client's HTML.
    const unknown = await page.request.get("/api/v1/definitely-not-a-route");
    expect(unknown.status()).toBe(404);
    expect(unknown.headers()["content-type"] ?? "").not.toContain("text/html");
  });

  test("a member signs in, chats, and the message survives a reload", async ({ page }) => {
    await signIn(page, MEMBER);
    await expect(page.locator(".chan", { hasText: "general" })).toBeVisible();
    const text = `hosted-${Math.random().toString(36).slice(2)}`;
    const input = page.locator(".composer input");
    await input.fill(text);
    await input.press("Enter");
    await expect(page.locator(".mbody", { hasText: text })).toBeVisible();
    await page.reload();
    await expect(page.locator(".mbody", { hasText: text })).toBeVisible();
  });

  test("a member stores, comments on, downloads and deletes a file", async ({ page }) => {
    await signIn(page, MEMBER);
    await page.goto("/files/");
    await expect(page.getByTestId("files-dropzone")).toBeVisible();
    const name = `hosted-${Math.random().toString(36).slice(2)}.txt`;
    const content = `hosted node file ${Date.now()}`;
    await page.getByTestId("files-input").setInputFiles({ name, mimeType: "text/plain", buffer: Buffer.from(content) });
    const row = page.getByTestId("file-row").filter({ hasText: name }).first();
    await expect(row).toBeVisible({ timeout: 45_000 });
    await expect(row.locator(".scanpill")).toHaveText("clean");

    // Commentary through the browser session (cookie + same-origin CSRF), then read it back.
    const files = (await (await page.request.get("/api/v1/files")).json()) as Array<{ id: string; name?: string; filename?: string }>;
    const stored = files.find((file) => (file.name ?? file.filename) === name);
    expect(stored, "the uploaded file is listed by the API").toBeTruthy();
    const note = `note-${Math.random().toString(36).slice(2)}`;
    const posted = await page.request.post(`/api/v1/files/${stored!.id}/comments`, {
      data: { body: note, visibility: "private" },
      headers: { Origin: new URL(BASE).origin },
    });
    expect(posted.status()).toBeLessThan(300);
    const comments = await (await page.request.get(`/api/v1/files/${stored!.id}/comments`)).text();
    expect(comments).toContain(note);

    const download = await page.request.get(`/api/v1/files/${stored!.id}/download`);
    expect(download.status()).toBe(200);
    expect(await download.text()).toBe(content);

    await row.getByLabel(`delete ${name}`).click();
    await row.getByLabel(`confirm delete ${name}`).click();
    await expect(page.getByTestId("file-row").filter({ hasText: name })).toHaveCount(0, { timeout: 20_000 });
  });

  test("Stream and Library show either real content or their own unavailable state", async ({ page }) => {
    await signIn(page, MEMBER);
    for (const area of ["stream", "library"] as const) {
      await page.goto(`/${area}/`);
      await expect(page.getByTestId("app-shell")).toBeVisible();
      // The shell and navigation must survive whatever the upstream is doing.
      await expect(page.getByRole("link", { name: "Chat" }).first()).toBeVisible();
      await expect(page.locator("body")).not.toContainText(/application error|unhandled|something went wrong/i);
    }
    await page.goto("/chat/");
    await expect(page.locator(".chan", { hasText: "general" })).toBeVisible();
  });

  test("settings and administration are reachable for the right roles", async ({ page, browser }) => {
    await signIn(page, MEMBER);
    await page.goto("/settings/");
    await expect(page.getByTestId("settings-page")).toBeVisible();
    await expect(page.getByText("Members", { exact: true })).toHaveCount(0);

    const ownerContext = await browser.newContext();
    const ownerPage = await ownerContext.newPage();
    await signIn(ownerPage, OWNER);
    await ownerPage.goto("/settings/");
    await expect(ownerPage.getByTestId("settings-page")).toBeVisible();
    await expect(ownerPage.getByRole("button", { name: "Members", exact: true }).first()).toBeVisible();
    await ownerContext.close();
  });

  test("logging out ends the session and signing in works again", async ({ page }) => {
    await signIn(page, MEMBER);
    await page.getByLabel("log out").click();
    await page.waitForURL("**/login/**");
    const me = await page.request.get("/api/v1/auth/me");
    expect(me.status()).toBe(401);
    await signIn(page, MEMBER);
    await expect(page.locator(".chan", { hasText: "general" })).toBeVisible();
  });
});
