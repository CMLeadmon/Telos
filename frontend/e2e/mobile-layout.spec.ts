import { test, expect, type Page } from "@playwright/test";

// Mobile layout matrix. Every authenticated route is checked across both phone
// profiles and both orientations for: no horizontal overflow, a reachable
// bottom nav, and comfortable (>=44px) primary touch targets. Touch emulation
// is forced so the pointer:coarse tap-target rules apply regardless of project.
test.use({ hasTouch: true, isMobile: true });

const USERNAME = process.env.E2E_USERNAME;
const PASSWORD = process.env.E2E_PASSWORD;

// Portrait + landscape for the two reference phones.
const VIEWPORTS = [
  { w: 390, h: 844, name: "iPhone portrait" },
  { w: 844, h: 390, name: "iPhone landscape" },
  { w: 412, h: 915, name: "Pixel portrait" },
  { w: 915, h: 412, name: "Pixel landscape" },
];

const ROUTES = ["/chat/", "/stream/", "/library/", "/library?view=files", "/settings/"];

async function login(page: Page) {
  await page.goto("/login/");
  await page.locator("#username").fill(USERNAME!);
  await page.locator("#password").fill(PASSWORD!);
  await page.getByRole("button", { name: "Enter the node" }).click();
  await page.waitForURL("**/chat/**");
}

async function hasHorizontalOverflow(page: Page): Promise<boolean> {
  return page.evaluate(() => {
    const doc = document.documentElement;
    // One px of tolerance for sub-pixel rounding.
    return doc.scrollWidth > doc.clientWidth + 1;
  });
}

// The public landing page needs no auth and must never overflow.
for (const vp of VIEWPORTS) {
  test(`landing has no horizontal overflow at ${vp.name}`, async ({ page }) => {
    await page.setViewportSize({ width: vp.w, height: vp.h });
    await page.goto("/");
    await page.waitForLoadState("domcontentloaded");
    expect(await hasHorizontalOverflow(page)).toBe(false);
  });
}

test.describe("authenticated mobile matrix", () => {
  test.skip(!USERNAME || !PASSWORD, "E2E_USERNAME/E2E_PASSWORD not set");

  for (const vp of VIEWPORTS) {
    for (const route of ROUTES) {
      test(`${route} at ${vp.name}: no overflow, nav reachable, 44px targets`, async ({
        page,
      }) => {
        await page.setViewportSize({ width: vp.w, height: vp.h });
        await login(page);
        await page.goto(route);
        await page.waitForLoadState("networkidle");

        // 1. No horizontal overflow.
        expect(await hasHorizontalOverflow(page)).toBe(false);

        // 2. The bottom nav is present and reachable (rail collapses below md).
        const nav = page.getByTestId("mobile-bottom-nav");
        await expect(nav).toBeVisible();

        // 3. Every bottom-nav button clears the 44px touch floor.
        const buttons = nav.locator(".mobile-nav-btn");
        const count = await buttons.count();
        expect(count).toBeGreaterThan(0);
        for (let i = 0; i < count; i++) {
          const box = await buttons.nth(i).boundingBox();
          expect(box, `nav button ${i} has a box`).not.toBeNull();
          expect(box!.height).toBeGreaterThanOrEqual(44);
        }
      });
    }
  }

  test("the chat composer stays visible above the bottom nav on a phone", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await login(page);
    await page.goto("/chat/");
    await page.waitForLoadState("networkidle");
    const composer = page.locator(".composer input");
    await expect(composer).toBeVisible();
    const box = await composer.boundingBox();
    expect(box).not.toBeNull();
    // The composer must sit within the viewport, not scrolled off the bottom.
    expect(box!.y).toBeLessThan(844);
  });
});
