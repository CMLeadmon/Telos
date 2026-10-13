import { expect, test, type Page, type Route } from "@playwright/test";

// A Jellyfin or Grimmory outage must degrade only its own module: the shell, the
// navigation and the other modules keep working, and the failing module says it is
// unavailable instead of crashing or showing a generic page error. The gateway maps
// an upstream failure to 502/503 (it never fabricates catalog records), so the
// outage is simulated at exactly that boundary.
const OUTAGE_BODY = {
  error: { code: "upstream_unavailable", message: "The media service is unavailable." },
};

async function mockNode(page: Page, failing: "stream" | "library") {
  await page.route("**/api/v1/**", async (route: Route) => {
    const path = new URL(route.request().url()).pathname;
    const json = (value: unknown, status = 200) => route.fulfill({ status, json: value });
    if (path === "/api/v1/auth/me") {
      return json({
        ID: "member-1", Username: "member", DisplayName: "Member", HasAvatar: false, Roles: ["Member"],
        Permissions: ["view_channel", "view_media", "view_library", "view_files"],
      });
    }
    if (path === "/api/v1/users/me/preferences") return json({});
    if (path === "/api/v1/channels") return json([{ id: "c1", name: "general", kind: "text" }]);
    if (path === "/api/v1/files") return json([]);
    // Chat's channel companions use object envelopes, not bare arrays.
    if (path.endsWith("/members")) return json({ online: [], count: 0 });
    if (path.endsWith("/pins")) return json({ pins: [] });
    if (failing === "stream" && path.startsWith("/api/v1/media")) return json(OUTAGE_BODY, 503);
    if (failing === "library" && path.startsWith("/api/v1/library")) return json(OUTAGE_BODY, 502);
    return json([]);
  });
}

async function expectShellIntact(page: Page) {
  await expect(page.getByTestId("app-shell")).toBeVisible();
  await expect(page.locator("body")).not.toContainText(/application error|unhandled runtime|something went wrong/i);
  for (const name of ["Chat", "Stream", "Library", "Files"]) {
    await expect(page.getByRole("link", { name, exact: true }).first()).toBeVisible();
  }
}

async function expectChatUsable(page: Page) {
  await page.goto("/chat/");
  await expectShellIntact(page);
  // The channel list lives in the rail on desktop and in the drawer on phones.
  const wide = (page.viewportSize()?.width ?? 0) > 900;
  if (wide) await expect(page.locator(".chan", { hasText: "general" })).toBeVisible();
  await expect(page).toHaveURL(/\/chat\//);
}

test("a Jellyfin outage degrades Stream and nothing else", async ({ page }) => {
  await mockNode(page, "stream");
  await page.goto("/stream/");
  await expectShellIntact(page);
  await expect(page.getByRole("heading", { name: /signal lost/i })).toBeVisible();

  await page.goto("/files/");
  await expectShellIntact(page);
  await expect(page.getByTestId("files-dropzone")).toBeVisible();

  await expectChatUsable(page);
});

test("a Grimmory outage degrades Library and nothing else", async ({ page }) => {
  await mockNode(page, "library");
  await page.goto("/library/");
  await expectShellIntact(page);
  await expect(page.getByRole("heading", { name: /shelf unreachable/i })).toBeVisible();

  await page.goto("/files/");
  await expectShellIntact(page);
  await expect(page.getByTestId("files-dropzone")).toBeVisible();

  await expectChatUsable(page);
});
