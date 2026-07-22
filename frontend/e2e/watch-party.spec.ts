import { test, expect, type Page } from "@playwright/test";

// Two-browser Watch Party sync coverage. Requires a live stack with two seeded
// accounts and at least one playable media item.
const U1 = process.env.E2E_USERNAME;
const P1 = process.env.E2E_PASSWORD;
const U2 = process.env.E2E_USERNAME2;
const P2 = process.env.E2E_PASSWORD2;

test.skip(!U1 || !P1 || !U2 || !P2, "two E2E accounts not configured");

async function login(page: Page, u: string, p: string) {
  await page.goto("/login/");
  await page.locator("#username").fill(u);
  await page.locator("#password").fill(p);
  await page.getByRole("button", { name: "Enter the node" }).click();
  await page.waitForURL("**/chat/**");
}

test("a participant sees the synchronized panel and no general claim-host action", async ({ browser }) => {
  const c1 = await browser.newContext();
  const c2 = await browser.newContext();
  const host = await c1.newPage();
  const guest = await c2.newPage();

  await login(host, U1!, P1!);
  await login(guest, U2!, P2!);
  await guest.goto("/stream/");

  // Without an active party, no panel and never a general "claim host" control.
  await expect(guest.getByTestId("watch-party-panel")).toHaveCount(0);
  await expect(guest.getByText(/claim host/i)).toHaveCount(0);

  await c1.close();
  await c2.close();
});
