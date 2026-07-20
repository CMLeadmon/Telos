import { expect, test, type Page } from "@playwright/test";

const VALID_PASSWORD = "a-valid-password-for-testing";

test.beforeEach(async ({ page }) => {
  await page.route("**/api/v1/auth/me", async (route) => {
    await route.fulfill({ status: 401, body: "Unauthorized" });
  });
});

async function fillCredentials(
  page: Page,
  username = "test-member",
  password = VALID_PASSWORD,
) {
  await page.getByLabel("username").fill(username);
  await page.getByLabel("password", { exact: true }).fill(password);
}

test("invite mode can be selected", async ({ page }) => {
  await page.goto("/login/");

  await page.getByRole("button", { name: "have an invite?" }).click();

  await expect(page.getByText("// redeem your invite")).toBeVisible();
  await expect(page.getByLabel("invite token")).toBeVisible();
  await expect(page.getByRole("button", { name: "Join the node" })).toBeVisible();
});

test("required-field feedback marks and focuses invalid inputs", async ({
  page,
}) => {
  await page.goto("/login/");

  await page.getByRole("button", { name: "Enter the node" }).click();

  await expect(page.locator("#auth-feedback")).toContainText(
    "transmission incomplete — fill every required field.",
  );
  await expect(page.getByLabel("username")).toHaveAttribute(
    "aria-invalid",
    "true",
  );
  await expect(page.getByLabel("password", { exact: true })).toHaveAttribute(
    "aria-invalid",
    "true",
  );
  await expect(page.getByLabel("username")).toBeFocused();
});

test("sign-in keeps short credentials, reports rejection, and retains input", async ({
  page,
}) => {
  let submittedUsername = "";
  await page.route("**/api/v1/auth/login", async (route) => {
    submittedUsername = (route.request().postDataJSON() as { username: string })
      .username;
    await route.fulfill({
      status: 401,
      body: "Unauthorized: Invalid credentials",
    });
  });
  await page.goto("/login/");
  await fillCredentials(page, "ab", "short");

  await page.getByRole("button", { name: "Enter the node" }).click();

  await expect(page.locator("#auth-feedback")).toContainText(
    "credentials rejected — check your username and passphrase.",
  );
  await expect(page.getByLabel("username")).toHaveAttribute(
    "aria-invalid",
    "true",
  );
  await expect(page.getByLabel("password", { exact: true })).toHaveAttribute(
    "aria-invalid",
    "true",
  );
  await expect(page.getByLabel("password", { exact: true })).toBeFocused();
  await expect(page.getByLabel("username")).toHaveValue("ab");
  await expect(page.getByLabel("password", { exact: true })).toHaveValue(
    "short",
  );
  expect(submittedUsername).toBe("ab");

  await page.getByLabel("password", { exact: true }).fill("still-here");
  await expect(page.locator("#auth-feedback")).toContainText(
    "credentials rejected",
  );
});

test("the eye control reveals and hides the password", async ({ page }) => {
  await page.goto("/login/");
  await page
    .getByLabel("password", { exact: true })
    .fill("secret-passphrase");

  await page.getByRole("button", { name: "Show password" }).click();
  await expect(
    page.getByLabel("password", { exact: true }),
  ).toHaveAttribute("type", "text");
  await expect(page.getByRole("button", { name: "Hide password" })).toHaveAttribute(
    "aria-pressed",
    "true",
  );

  await page.getByRole("button", { name: "Hide password" }).click();
  await expect(
    page.getByLabel("password", { exact: true }),
  ).toHaveAttribute("type", "password");
});

for (const accountMode of [
  {
    name: "first boot",
    modeButton: "first boot",
    tokenLabel: "bootstrap token",
    submitButton: "Bootstrap owner",
  },
  {
    name: "invite",
    modeButton: "have an invite?",
    tokenLabel: "invite token",
    submitButton: "Join the node",
  },
]) {
  test(`${accountMode.name} reports an invalid passphrase`, async ({ page }) => {
    await page.goto("/login/");
    await page.getByRole("button", { name: accountMode.modeButton }).click();
    await fillCredentials(page, "new-member", "too-short");
    await page.getByLabel(accountMode.tokenLabel).fill("test-token");

    await page.getByRole("button", { name: accountMode.submitButton }).click();

    await expect(page.locator("#auth-feedback")).toContainText(
      "passphrase rejected — use 15–128 characters.",
    );
    await expect(
      page.getByLabel("password", { exact: true }),
    ).toHaveAttribute(
      "aria-invalid",
      "true",
    );
    await expect(
      page.getByLabel("password", { exact: true }),
    ).toBeFocused();
  });
}

test("bootstrap reports and focuses a rejected token", async ({ page }) => {
  await page.route("**/api/v1/auth/bootstrap", async (route) => {
    await route.fulfill({
      status: 403,
      body: "Forbidden: Invalid bootstrap token",
    });
  });
  await page.goto("/login/");
  await page.getByRole("button", { name: "first boot" }).click();
  await fillCredentials(page, "new-owner");
  await page.getByLabel("bootstrap token").fill("wrong-token");

  await page.getByRole("button", { name: "Bootstrap owner" }).click();

  await expect(page.locator("#auth-feedback")).toContainText(
    "bootstrap token rejected — check the token and try again.",
  );
  await expect(page.getByLabel("bootstrap token")).toHaveAttribute(
    "aria-invalid",
    "true",
  );
  await expect(page.getByLabel("bootstrap token")).toBeFocused();
});

test("invite reports progress, prevents resubmission, and explains token failure", async ({
  page,
}) => {
  let requests = 0;
  await page.route("**/api/v1/auth/invites/accept", async (route) => {
    requests += 1;
    await new Promise((resolve) => setTimeout(resolve, 200));
    await route.fulfill({
      status: 400,
      body: "Invalid or expired invite token",
    });
  });
  await page.goto("/login/");
  await page.getByRole("button", { name: "have an invite?" }).click();
  await fillCredentials(page, "new-member");
  await page.getByLabel("invite token").fill("expired-token");

  await page.getByRole("button", { name: "Join the node" }).click();

  const busyButton = page.getByRole("button", { name: "Joining…" });
  await expect(busyButton).toBeDisabled();
  await expect(page.getByRole("status")).toContainText(
    "redeeming invite — hold the line.",
  );
  await busyButton.click({ force: true });
  await expect(page.locator("#auth-feedback")).toContainText(
    "invite rejected — the token is invalid or expired.",
  );
  await expect(page.getByLabel("invite token")).toHaveAttribute(
    "aria-invalid",
    "true",
  );
  expect(requests).toBe(1);
});

for (const gatewayCase of [
  {
    name: "disabled account",
    status: 401,
    body: "Unauthorized: Account disabled",
    copy: "access denied — this account is disabled. Contact your node owner.",
  },
  {
    name: "timeout",
    status: 408,
    body: "Request Timeout",
    copy: "signal timed out — check your connection and try again.",
  },
  {
    name: "rate limit",
    status: 429,
    body: "Too Many Requests",
    copy: "too many attempts — stand down briefly, then try again.",
  },
  {
    name: "server fault",
    status: 500,
    body: "Internal Server Error",
    copy: "node fault — the server could not complete the request. Try again.",
  },
]) {
  test(`sign-in translates the ${gatewayCase.name} response`, async ({
    page,
  }) => {
    await page.route("**/api/v1/auth/login", async (route) => {
      await route.fulfill({
        status: gatewayCase.status,
        body: gatewayCase.body,
      });
    });
    await page.goto("/login/");
    await fillCredentials(page);

    await page.getByRole("button", { name: "Enter the node" }).click();

    await expect(page.locator("#auth-feedback")).toContainText(
      gatewayCase.copy,
    );
  });
}

test("sign-in explains when the node cannot be reached", async ({ page }) => {
  await page.route("**/api/v1/auth/login", async (route) => {
    await route.abort("connectionrefused");
  });
  await page.goto("/login/");
  await fillCredentials(page);

  await page.getByRole("button", { name: "Enter the node" }).click();

  await expect(page.locator("#auth-feedback")).toContainText(
    "node unreachable — check your connection and the server address, then try again.",
  );
});

for (const successMode of [
  {
    name: "sign-in",
    modeButton: null,
    tokenLabel: null,
    endpoint: "login",
    submitButton: "Enter the node",
  },
  {
    name: "first boot",
    modeButton: "first boot",
    tokenLabel: "bootstrap token",
    endpoint: "bootstrap",
    submitButton: "Bootstrap owner",
  },
  {
    name: "invite",
    modeButton: "have an invite?",
    tokenLabel: "invite token",
    endpoint: "invites/accept",
    submitButton: "Join the node",
  },
]) {
  test(`${successMode.name} reports success before redirecting`, async ({
    page,
  }) => {
    let sessionReady = false;
    await page.unroute("**/api/v1/auth/me");
    await page.route("**/api/v1/auth/me", async (route) => {
      if (!sessionReady) {
        await route.fulfill({ status: 401, body: "Unauthorized" });
        return;
      }
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
    await page.route("**/api/v1/auth/login", async (route) => {
      sessionReady = true;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ status: "success" }),
      });
    });
    if (successMode.endpoint !== "login") {
      await page.route(`**/api/v1/auth/${successMode.endpoint}`, async (route) => {
        await route.fulfill({
          status: 201,
          contentType: "application/json",
          body: JSON.stringify({ status: "success" }),
        });
      });
    }

    await page.goto("/login/");
    if (successMode.modeButton) {
      await page.getByRole("button", { name: successMode.modeButton }).click();
    }
    await fillCredentials(page);
    if (successMode.tokenLabel) {
      await page.getByLabel(successMode.tokenLabel).fill("valid-token");
    }

    await page
      .getByRole("button", { name: successMode.submitButton })
      .click();

    await expect(page.getByRole("status")).toContainText(
      "access accepted — entering the node.",
    );
    await page.waitForURL("**/chat/**");
  });
}
