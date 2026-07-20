import { expect, test, type Page, type Route } from "@playwright/test";

interface MockScanOptions {
  outcome: "complete" | "failed";
}

async function mockStreamNode(page: Page, options: MockScanOptions) {
  let scanStarted = false;
  let mediaDeleted = false;

  await page.route("**/api/v1/**", async (route: Route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;

    if (path === "/api/v1/auth/me") {
      await route.fulfill({
        json: {
          ID: "owner-1",
          Username: "owner",
          DisplayName: "Node Owner",
          HasAvatar: false,
          Roles: ["Owner"],
        },
      });
      return;
    }
    if (path === "/api/v1/users/me/preferences") {
      await route.fulfill({ json: {} });
      return;
    }
    if (path === "/api/v1/channels") {
      await route.fulfill({ json: [] });
      return;
    }
    if (path === "/api/v1/media/refresh" && request.method() === "POST") {
      scanStarted = true;
      await route.fulfill({
        status: 202,
        json: { status: "scanning", message: "Jellyfin is scanning." },
      });
      return;
    }
    if (path === "/api/v1/media/refresh/status") {
      if (options.outcome === "complete") {
        mediaDeleted = true;
        await route.fulfill({
          json: { status: "complete", message: "Jellyfin scan complete." },
        });
      } else {
        await route.fulfill({
          json: { status: "failed", message: "Jellyfin scan failed." },
        });
      }
      return;
    }
    if (path === "/api/v1/media") {
      await route.fulfill({
        json: [{ id: "movies", name: "Movies", type: "video" }],
      });
      return;
    }
    if (path === "/api/v1/media/items") {
      await route.fulfill({
        json: mediaDeleted
          ? []
          : [
              {
                id: "deleted-film",
                title: "Soon to Be Gone",
                duration: "1h 30m",
                type: "Movie",
                isFolder: false,
              },
            ],
      });
      return;
    }

    await route.fulfill({ status: 404, body: "not found" });
  });

  await page.goto("/stream/");
  await expect(page.getByText("Soon to Be Gone").first()).toBeVisible();
  await page.getByTestId("stream-refresh").click();
  await expect.poll(() => scanStarted).toBe(true);
}

test("completed Jellyfin scan shows success and removes deleted media", async ({
  page,
}) => {
  await mockStreamNode(page, { outcome: "complete" });

  await expect(page.getByTestId("stream-refresh-notice")).toContainText(
    "Jellyfin scan complete.",
  );
  await expect(page.getByText("Soon to Be Gone")).toHaveCount(0);
  await expect(page.getByTestId("stream-refresh")).toContainText("scan complete");
});

test("failed Jellyfin scan keeps the existing catalog visible", async ({ page }) => {
  await mockStreamNode(page, { outcome: "failed" });

  await expect(page.getByTestId("stream-refresh-notice")).toContainText(
    "Jellyfin scan failed.",
  );
  await expect(page.getByText("Soon to Be Gone").first()).toBeVisible();
});
