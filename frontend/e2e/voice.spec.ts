import { test, expect, type Page } from "@playwright/test";

// Credentialed run against a live gateway:
//   E2E_USERNAME=<user> E2E_PASSWORD=<pass> npx playwright test voice
const USERNAME = process.env.E2E_USERNAME;
const PASSWORD = process.env.E2E_PASSWORD;

test.skip(!USERNAME || !PASSWORD, "E2E_USERNAME/E2E_PASSWORD not set");

// Fake devices: mic emits a tone, permission prompts auto-accept.
test.use({
  launchOptions: {
    args: [
      "--use-fake-device-for-media-stream",
      "--use-fake-ui-for-media-stream",
    ],
  },
});

async function login(page: Page) {
  await page.goto("/login/");
  await page.locator("#username").fill(USERNAME!);
  await page.locator("#password").fill(PASSWORD!);
  await page.getByRole("button", { name: "Enter the node" }).click();
  await page.waitForURL("**/chat/**");
}

test("voice settings list devices and the mic meter reacts", async ({
  page,
}) => {
  await login(page);
  await page.goto("/settings/");
  await page.getByRole("button", { name: "Voice & Audio" }).click();

  await page.getByTestId("voice-devices-enable").click();
  await expect(page.getByTestId("voice-input-select")).toBeVisible();
  // Chromium's fake mic enumerates as "Fake Audio Input 1".
  await expect(page.getByTestId("voice-input-select")).toContainText(
    /Fake|Default/,
  );

  await page.getByRole("button", { name: "mic test" }).click();
  // The fake device plays a tone, so the RMS level must rise above zero.
  await expect
    .poll(
      async () =>
        Number(
          await page
            .getByTestId("voice-mic-meter")
            .getAttribute("data-level"),
        ),
      { timeout: 5_000 },
    )
    .toBeGreaterThan(0);
});

test("joining a voice channel shows the dock (or a surfaced error)", async ({
  page,
}) => {
  await login(page);
  // Channels load asynchronously after landing on chat; wait before deciding.
  const voiceChannel = page
    .locator(".chan", { has: page.locator(".joinlbl") })
    .first();
  await voiceChannel
    .waitFor({ state: "visible", timeout: 10_000 })
    .catch(() => {});
  test.skip(!(await voiceChannel.count()), "no voice channels provisioned");

  await voiceChannel.click();
  // The dock must appear for connecting, connected, AND error states —
  // silent failure is the bug this feature fixes.
  const dock = page.getByTestId("voice-dock");
  await expect(dock).toBeVisible({ timeout: 15_000 });

  if (await page.getByTestId("voice-dock-error").isVisible()) {
    await expect(page.getByTestId("voice-dock-error")).not.toBeEmpty();
    return; // LiveKit not reachable in this environment; error surfacing verified
  }

  // Connected: exercise mute, deafen, leave.
  await page.getByLabel("mute microphone").click();
  await expect(page.getByLabel("unmute microphone")).toBeVisible();
  await page.getByLabel("deafen").click();
  await expect(page.getByLabel("undeafen")).toBeVisible();
  await page.getByLabel("undeafen").click();
  await page.getByLabel("leave voice").click();
  await expect(dock).toBeHidden();
});
