import { test, expect } from "@playwright/test";

const VIEWPORTS = [
  { width: 390, height: 844, name: "iPhone 12/13 Portrait" },
  { width: 412, height: 915, name: "Pixel 7 Portrait" },
  { width: 844, height: 390, name: "iPhone 12/13 Landscape" },
  { width: 915, height: 412, name: "Pixel 7 Landscape" },
  { width: 768, height: 1024, name: "Tablet Portrait" },
  { width: 1024, height: 768, name: "Tablet Landscape" },
  { width: 1440, height: 900, name: "Desktop" },
];

for (const vp of VIEWPORTS) {
  test(`no horizontal overflow on landing page at ${vp.width}x${vp.height} (${vp.name})`, async ({ page }) => {
    await page.setViewportSize({ width: vp.width, height: vp.height });
    await page.goto("/");
    await page.waitForLoadState("domcontentloaded");

    const overflow = await page.evaluate(() => {
      const docEl = document.documentElement;
      return docEl.scrollWidth > docEl.clientWidth;
    });

    expect(overflow).toBe(false);
  });
}
