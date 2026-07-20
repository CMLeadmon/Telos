import { networkInterfaces } from "node:os";
import { expect, test } from "@playwright/test";

const VOICE_CHANNEL_ID = "00000000-0000-0000-0000-000000000005";
const HTTPS_ERROR =
  "Voice requires a secure HTTPS connection. Reopen Telos using its HTTPS address and try again.";

function insecureBaseURL(): string {
  if (process.env.E2E_INSECURE_BASE_URL) {
    return process.env.E2E_INSECURE_BASE_URL;
  }
  for (const addresses of Object.values(networkInterfaces())) {
    const address = addresses?.find(
      (candidate) => candidate.family === "IPv4" && !candidate.internal,
    );
    if (address) return `http://${address.address}:3000`;
  }
  throw new Error(
    "No non-loopback IPv4 address found; set E2E_INSECURE_BASE_URL.",
  );
}

test.use({
  baseURL: insecureBaseURL(),
});

test("insecure voice join fails before requesting a token", async ({ page }) => {
  let tokenRequests = 0;

  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;

    if (path === "/api/v1/auth/me") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          ID: "00000000-0000-0000-0000-000000000001",
          Username: "voice-test",
          Roles: ["Member"],
          Permissions: ["join_voice"],
          DisplayName: "Voice Test",
          HasAvatar: false,
        }),
      });
      return;
    }

    if (path === "/api/v1/users/me/preferences") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: "{}",
      });
      return;
    }

    if (path === "/api/v1/channels") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify([
          { id: VOICE_CHANNEL_ID, name: "Lounge", type: "voice" },
        ]),
      });
      return;
    }

    if (path === `/api/v1/voice/channels/${VOICE_CHANNEL_ID}/token`) {
      tokenRequests += 1;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ token: "must-not-be-requested" }),
      });
      return;
    }

    await route.fulfill({ status: 404, body: "not mocked" });
  });

  await page.goto("/chat/");
  await expect(page.getByTestId("app-shell")).toBeVisible();
  await expect
    .poll(() => page.evaluate(() => globalThis.isSecureContext))
    .toBe(false);

  await page.locator(".chan", { hasText: "Lounge" }).click();

  await expect(page.getByTestId("voice-dock-error")).toHaveText(HTTPS_ERROR);
  expect(tokenRequests).toBe(0);
});
