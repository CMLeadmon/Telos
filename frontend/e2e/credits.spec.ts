import { expect, test } from "@playwright/test";

const ISOLATED_SERVICES = [
  "Jellyfin",
  "Grimmory",
  "LiveKit",
  "Traefik",
  "PostgreSQL",
  "Redis",
  "MariaDB",
  "ClamAV",
];

test.beforeEach(async ({ page }) => {
  await page.route("**/api/v1/auth/me", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        ID: "user-1",
        Username: "test-member",
        Roles: ["Member"],
        Permissions: [],
        DisplayName: "",
        HasAvatar: false,
      }),
    });
  });
});

test("an authenticated member can open Settings → Credits and find every isolated service", async ({
  page,
}) => {
  await page.goto("/settings/");
  await page.getByRole("button", { name: "Credits" }).click();

  const section = page.getByTestId("credits-section");
  await expect(section).toBeVisible();

  for (const name of ISOLATED_SERVICES) {
    const entry = section.getByTestId(`credit-${name.toLowerCase()}`);
    await expect(entry).toContainText(name);

    const sourceLink = entry.getByRole("link", { name: /source/i });
    const sourceHref = await sourceLink.getAttribute("href");
    expect(sourceHref).toMatch(/^https:\/\//);

    const licenseLink = entry.getByRole("link", { name: /license/i });
    const licenseHref = await licenseLink.getAttribute("href");
    expect(licenseHref).toMatch(/^https:\/\//);
  }
});

test("Credits states the Telos gateway/client license is Apache-2.0", async ({
  page,
}) => {
  await page.goto("/settings/");
  await page.getByRole("button", { name: "Credits" }).click();

  await expect(page.getByTestId("credits-section")).toContainText(
    "Apache License 2.0",
  );
});
