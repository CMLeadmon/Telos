# Voice Secure-Context Guard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> `superpowers:executing-plans` to implement this plan task-by-task. Do not use
> subagents for this repository task.

**Goal:** Reject remote plain-HTTP voice joins before token acquisition, show
an actionable HTTPS-required error, and document the supported deployment
boundary.

**Architecture:** A pure capability evaluator in `voiceAudio.ts` owns the
secure-context policy, while a browser adapter reads the actual globals. The
voice store runs that preflight before token acquisition. Unit tests cover the
policy and a mocked Playwright flow on a non-loopback hostname proves that the
UI displays the error without sending a token request.

**Tech Stack:** TypeScript, Zustand, LiveKit Client, Node test runner,
Playwright, Next.js 16.

## Global Constraints

- Preserve every unrelated tracked and untracked worktree change.
- Do not modify `.env`, DNS, certificates, router forwarding, or firewall
  state.
- Do not add dependencies.
- Do not bypass browser secure-context requirements.
- Keep Traefik as the production HTTPS ingress.
- Do not commit or push; the accepted implementation prompt explicitly
  reserves those actions.

---

### Task 1: Secure-context capability policy

**Files:**
- Create: `frontend/src/lib/voiceAudio.test.mjs`
- Modify: `frontend/src/lib/voiceAudio.ts`
- Modify: `frontend/package.json`

**Interfaces:**
- Produces:
  `VOICE_HTTPS_REQUIRED_MESSAGE: string`,
  `VoiceCaptureEnvironment`,
  `voiceCaptureEnvironmentError(environment): string | null`, and
  `currentVoiceCaptureEnvironmentError(): string | null`.
- Consumes: browser `globalThis.isSecureContext` and
  `navigator.mediaDevices.getUserMedia`.

- [ ] **Step 1: Add the focused test script**

Add this script to `frontend/package.json`:

```json
"test:unit": "node --disable-warning=MODULE_TYPELESS_PACKAGE_JSON --experimental-strip-types --test src/lib/*.test.mjs"
```

- [ ] **Step 2: Write the failing policy tests**

Create `frontend/src/lib/voiceAudio.test.mjs`:

```javascript
import assert from "node:assert/strict";
import test from "node:test";

import {
  VOICE_HTTPS_REQUIRED_MESSAGE,
  voiceCaptureEnvironmentError,
} from "./voiceAudio.ts";

test("allows microphone capture from a secure capable environment", () => {
  assert.equal(
    voiceCaptureEnvironmentError({
      isSecureContext: true,
      hasGetUserMedia: true,
    }),
    null,
  );
});

test("requires HTTPS in an insecure context", () => {
  assert.equal(
    voiceCaptureEnvironmentError({
      isSecureContext: false,
      hasGetUserMedia: true,
    }),
    VOICE_HTTPS_REQUIRED_MESSAGE,
  );
});

test("requires HTTPS when getUserMedia is unavailable", () => {
  assert.equal(
    voiceCaptureEnvironmentError({
      isSecureContext: true,
      hasGetUserMedia: false,
    }),
    VOICE_HTTPS_REQUIRED_MESSAGE,
  );
});
```

- [ ] **Step 3: Run the unit tests and verify RED**

Run:

```bash
cd frontend && npm run test:unit
```

Expected: FAIL because `VOICE_HTTPS_REQUIRED_MESSAGE` and
`voiceCaptureEnvironmentError` are not exported.

- [ ] **Step 4: Implement the minimal capability policy**

Add near the top of `frontend/src/lib/voiceAudio.ts`:

```typescript
export const VOICE_HTTPS_REQUIRED_MESSAGE =
  "Voice requires a secure HTTPS connection. Reopen Telos using its HTTPS address and try again.";

export interface VoiceCaptureEnvironment {
  isSecureContext: boolean;
  hasGetUserMedia: boolean;
}

export function voiceCaptureEnvironmentError(
  environment: VoiceCaptureEnvironment,
): string | null {
  if (!environment.isSecureContext || !environment.hasGetUserMedia) {
    return VOICE_HTTPS_REQUIRED_MESSAGE;
  }
  return null;
}

export function currentVoiceCaptureEnvironmentError(): string | null {
  return voiceCaptureEnvironmentError({
    isSecureContext: globalThis.isSecureContext === true,
    hasGetUserMedia:
      typeof navigator !== "undefined" &&
      typeof navigator.mediaDevices?.getUserMedia === "function",
  });
}
```

- [ ] **Step 5: Run the unit tests and verify GREEN**

Run:

```bash
cd frontend && npm run test:unit
```

Expected: three passing tests and zero failures.

### Task 2: Block the voice join before token acquisition

**Files:**
- Create: `frontend/e2e/voice-secure-context.spec.ts`
- Modify: `frontend/src/stores/useVoiceSessionStore.ts`

**Interfaces:**
- Consumes: `currentVoiceCaptureEnvironmentError(): string | null`.
- Produces: insecure clients enter the existing voice-dock error state without
  calling `/api/v1/voice/channels/{id}/token`.

- [ ] **Step 1: Write the failing browser integration test**

Create `frontend/e2e/voice-secure-context.spec.ts`:

```typescript
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
```

- [ ] **Step 2: Run the integration test and verify RED**

With `npm run dev` already running on port 3000, run:

```bash
cd frontend && npx playwright test e2e/voice-secure-context.spec.ts
```

Expected: FAIL because the current store requests the token and attempts
LiveKit before microphone capability is checked.

- [ ] **Step 3: Add the preflight to the voice store**

Extend the `voiceAudio` import in
`frontend/src/stores/useVoiceSessionStore.ts`:

```typescript
import {
  createMicPipeline,
  currentVoiceCaptureEnvironmentError,
  voiceErrorMessage,
  type MicPipeline,
} from "@/lib/voiceAudio";
```

Change the start of `join` to:

```typescript
join: async (channelId) => {
  await get().leave();
  const environmentError = currentVoiceCaptureEnvironmentError();
  if (environmentError) {
    set({
      status: "error",
      channelId,
      error: environmentError,
    });
    return;
  }
  set({ status: "connecting", channelId, error: null });
```

Keep the remainder of the join flow unchanged.

- [ ] **Step 4: Run the integration test and verify GREEN**

Run:

```bash
cd frontend && npx playwright test e2e/voice-secure-context.spec.ts
```

Expected: one passing test; the error copy is visible and the token request
counter remains zero.

- [ ] **Step 5: Re-run the focused unit tests**

Run:

```bash
cd frontend && npm run test:unit
```

Expected: three passing tests and zero failures.

### Task 3: Correct the deployment documentation

**Files:**
- Modify: `frontend/AGENTS.md`
- Modify: `documentation/architecture/02-deployment.md`

**Interfaces:**
- Consumes: the existing production HTTPS and development port-3000
  architecture.
- Produces: explicit guidance distinguishing page reachability from browser
  microphone capability.

- [ ] **Step 1: Update the frontend development guide**

Immediately after the `TELOS_DEV_ORIGINS` paragraph in `frontend/AGENTS.md`,
add:

```markdown
Port 3000 is plain HTTP. Browsers allow microphone capture on HTTP localhost as
a development exception, but not on LAN IPs, Tailscale IPs, public IPs, or
ordinary hostnames. Remote clients can render the development app over port
3000 but voice requires a trusted HTTPS proxy or tunnel. Production users must
use the Traefik-served `https://${TELOS_DOMAIN}` origin.
```

- [ ] **Step 2: Qualify the deployment specification**

After the development-server command in
`documentation/architecture/02-deployment.md`, add:

```markdown
This port-3000 path provides page, API, and signaling reachability only. Plain
HTTP on a non-loopback origin is not a browser secure context, so microphone
capture and voice publishing are unavailable. Use localhost for local voice
development or place a trusted HTTPS proxy or tunnel in front of the
development server for a remote test. Never expose port 3000 as the production
entry point.
```

In the same section, ensure the production paragraph continues to require
Traefik HTTPS and leaves DNS, certificate, and router changes as operator
actions.

- [ ] **Step 3: Check documentation for contradictions and placeholders**

Run:

```bash
rg -n "only need access to port 3000|port 3000|secure context|trusted HTTPS" \
  frontend/AGENTS.md documentation/architecture/02-deployment.md
rg -n "ci""te:|TO""DO|TB""D" documentation/ AGENTS.md
```

Expected: the port-3000 statement is immediately qualified, the secure-context
guidance appears in both files, and no citation or placeholder markers are
found.

### Task 4: Regression and deployment-boundary verification

**Files:**
- Verify only; no planned production edits.

**Interfaces:**
- Consumes all changes from Tasks 1–3.
- Produces fresh evidence for code quality, browser behavior, and unchanged
  infrastructure syntax.

- [ ] **Step 1: Run focused frontend verification**

Run:

```bash
cd frontend
npm run test:unit
npm run lint
npm run build
npx playwright test e2e/voice-secure-context.spec.ts
```

Expected: every command exits zero.

- [ ] **Step 2: Run existing voice coverage**

Run:

```bash
cd frontend && npx playwright test e2e/voice.spec.ts
```

Expected: tests pass when `E2E_USERNAME` and `E2E_PASSWORD` are configured, or
are explicitly skipped by their existing credential gate.

- [ ] **Step 3: Validate the browser capability boundary directly**

Use Playwright against the running development server to evaluate
`isSecureContext`, `navigator.mediaDevices`, and `getUserMedia` at loopback and
at a non-loopback hostname mapped to `127.0.0.1`.

Expected:

```text
http://127.0.0.1:3000: secure, mediaDevices present, getUserMedia function
http://telos.test:3000: insecure, mediaDevices absent, getUserMedia undefined
```

- [ ] **Step 4: Run the repository verification suite**

Run:

```bash
grep -rn "ci""te:" documentation/ AGENTS.md ; echo "exit=$?"
grep -rnE "TO""DO|TB""D" documentation/ AGENTS.md ; echo "exit=$?"
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go test ./...
cd frontend && npm run lint && npx playwright test
```

Expected: documentation searches report no matches, backend tests pass, lint
passes, the secure-context integration test passes, credential-gated live tests
either pass or report their documented skips, and no unrelated regression is
introduced.

- [ ] **Step 5: Review the final diff**

Run:

```bash
git diff --check
git status --short
git diff -- frontend/src/lib/voiceAudio.ts \
  frontend/src/lib/voiceAudio.test.mjs \
  frontend/src/stores/useVoiceSessionStore.ts \
  frontend/e2e/voice-secure-context.spec.ts \
  frontend/package.json \
  frontend/AGENTS.md \
  documentation/architecture/02-deployment.md \
  docs/superpowers/specs/2026-07-17-voice-secure-context-design.md \
  docs/superpowers/plans/2026-07-17-voice-secure-context.md
```

Expected: no whitespace errors, only scoped voice-fix changes in the reviewed
diff, and every pre-existing unrelated worktree change remains untouched.
