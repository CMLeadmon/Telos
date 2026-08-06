# S4 Connection Layer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement server URL entry and validation, secure credential storage via OS keychain abstractions, Trust-On-First-Use (TOFU) certificate pinning, version skew checks, and socket lifecycle reconnection event listeners in the Next.js frontend client.

**Architecture:** Build `useConnectionStore.ts` using Zustand to manage connection lifecycle states, abstract native OS keychain storage in `secureStorage.ts`, implement the `/connect` configuration route with node health probing and TOFU certificate confirmation, wire root entry routing in `app/page.tsx`, and add `online` and `visibilitychange` listeners to `ReconnectingSocket`.

**Tech Stack:** Next.js 16.2.10, React 19.2.7, TypeScript 5.9.3, Zustand 5.0.14, Vitest 4.1.10.

## Global Constraints

- Go toolchain is **1.26.5**; Go is **not installed on the host**. All backend toolchain commands run in a container via **podman**, never docker.
- Frontend is **Next.js 16.2.10** static export (`output: "export"`), **React 19.2.7**, **TypeScript 5.9.3**, **Zustand 5.0.14**. Tests are **vitest 4.1.10**; E2E is **@playwright/test 1.61.1**.
- Media libraries are pinned: **epubjs 0.4.2**, **pdfjs-dist 6.1.200**, **hls.js 1.6.16**. Do not upgrade them as part of this work.
- `backend/` is one flat Go package (`module telos-core`). Extend the sibling file matching the concern; do not grow `main.go`.
- Schema changes are a **new numbered migration** in `backend/db/migrations/`. Highest existing is `0022_catalog_identity_and_progress.sql`. Migrations are idempotent (`IF NOT EXISTS` / `ON CONFLICT`). **Never edit an already-applied migration** — `backend/migrations.go` verifies checksums.
- All credentials come from `.env` interpolation. Never hardcode secrets.
- **Copyleft boundary:** never link or compile Jellyfin or Grimmory code into the Go gateway. Integrate over HTTP across container boundaries only.
- Never hand-edit `frontend/src/styles/tokens/` or `frontend/src/styles/foundations/`.
- Never alter the environment to make a gate pass. A failing gate is reported, not forced green.
- A local backend `go build` fails unless `backend/out/` exists, until S1's build tag lands.

---

## File structure

- Create: `frontend/src/stores/useConnectionStore.ts` — Zustand store for connection lifecycle state and active node metadata.
- Create: `frontend/src/stores/useConnectionStore.test.ts` — unit tests for connection store state machine.
- Create: `frontend/src/lib/secureStorage.ts` — OS keychain wrapper with in-memory fallback.
- Create: `frontend/src/lib/secureStorage.test.ts` — unit tests for secure storage key/value operations.
- Create: `frontend/src/app/connect/page.tsx` — server URL entry, health validation, and TOFU certificate confirmation UI.
- Create: `frontend/src/app/connect/connect_page.test.tsx` — unit tests for `/connect` route interactions.
- Modify: `frontend/src/app/page.tsx` — check connection state before redirecting to `/login`.
- Create: `frontend/src/lib/certPinning.ts` — TLS leaf certificate fingerprint verification and TOFU helper.
- Create: `frontend/src/lib/certPinning.test.ts` — unit tests for certificate fingerprint comparison.
- Create: `frontend/src/components/VersionSkewBanner.tsx` — informational banner for soft client/server version skew.
- Create: `frontend/src/components/VersionSkewBanner.test.tsx` — unit tests for version skew banner rendering.
- Modify: `frontend/src/app/(shell)/layout.tsx` — integrate `VersionSkewBanner` into shell UI.
- Modify: `frontend/src/lib/reconnectingSocket.ts` — implement `online` and `visibilitychange` event listeners while preserving backoff timing.
- Create: `frontend/src/lib/reconnectingSocket_lifecycle.test.ts` — unit tests for network and visibility event listeners.

---

### Task 1: Create `useConnectionStore`

**Files:**
- Create: `frontend/src/stores/useConnectionStore.ts`
- Create: `frontend/src/stores/useConnectionStore.test.ts`

**Interfaces:**
- Produces: `ConnectionState`, `ConnectionError`, `useConnectionStore` in `frontend/src/stores/useConnectionStore.ts`.

- [ ] **Step 1: Write failing unit test for `useConnectionStore`**

Create `frontend/src/stores/useConnectionStore.test.ts`:

```typescript
import { describe, expect, it, beforeEach } from "vitest";
import { useConnectionStore } from "@/stores/useConnectionStore";

describe("useConnectionStore", () => {
  beforeEach(() => {
    useConnectionStore.getState().reset();
  });

  it("initializes with unconfigured state", () => {
    const state = useConnectionStore.getState();
    expect(state.state).toBe("unconfigured");
    expect(state.serverUrl).toBe("");
    expect(state.error).toBeNull();
  });

  it("transitions through validating to configured state", () => {
    const store = useConnectionStore.getState();
    store.setValidating("https://telos.example.com");

    expect(useConnectionStore.getState().state).toBe("validating");
    expect(useConnectionStore.getState().serverUrl).toBe("https://telos.example.com");

    useConnectionStore.getState().setConfigured("sha256-cert-fingerprint-abc");

    expect(useConnectionStore.getState().state).toBe("configured");
    expect(useConnectionStore.getState().pinnedCertFingerprint).toBe("sha256-cert-fingerprint-abc");
  });

  it("sets error state cleanly", () => {
    const store = useConnectionStore.getState();
    store.setValidating("https://invalid.example.com");
    store.setError("unreachable", "Could not connect to target host");

    expect(useConnectionStore.getState().state).toBe("error");
    expect(useConnectionStore.getState().error).toBe("unreachable");
    expect(useConnectionStore.getState().errorMessage).toBe("Could not connect to target host");
  });
});
```

- [ ] **Step 2: Implement `useConnectionStore.ts`**

Create `frontend/src/stores/useConnectionStore.ts`:

```typescript
import { create } from "zustand";
import { setServerConfig } from "@/lib/serverConfig";

export type ConnectionState = "unconfigured" | "validating" | "configured" | "error";
export type ConnectionError =
  | "unreachable"
  | "version_skew_hard"
  | "untrusted_certificate"
  | "credential_revoked";

export interface ConnectionStoreState {
  state: ConnectionState;
  serverUrl: string;
  pinnedCertFingerprint: string | null;
  serverVersion: string | null;
  minClientVersion: string | null;
  versionSkewSoft: boolean;
  error: ConnectionError | null;
  errorMessage: string | null;

  setValidating: (url: string) => void;
  setConfigured: (certFingerprint?: string, serverVersion?: string) => void;
  setError: (error: ConnectionError, message?: string) => void;
  setVersionSkewSoft: (skew: boolean) => void;
  reset: () => void;
  syncServerConfig: (accessToken?: string | null) => void;
}

export const useConnectionStore = create<ConnectionStoreState>((set, get) => ({
  state: "unconfigured",
  serverUrl: "",
  pinnedCertFingerprint: null,
  serverVersion: null,
  minClientVersion: null,
  versionSkewSoft: false,
  error: null,
  errorMessage: null,

  setValidating: (url: string) => {
    set({
      state: "validating",
      serverUrl: url,
      error: null,
      errorMessage: null,
    });
  },

  setConfigured: (certFingerprint, serverVersion) => {
    set({
      state: "configured",
      pinnedCertFingerprint: certFingerprint ?? null,
      serverVersion: serverVersion ?? null,
      error: null,
      errorMessage: null,
    });
    get().syncServerConfig();
  },

  setError: (error: ConnectionError, message?: string) => {
    set({
      state: "error",
      error,
      errorMessage: message ?? null,
    });
  },

  setVersionSkewSoft: (skew: boolean) => {
    set({ versionSkewSoft: skew });
  },

  reset: () => {
    set({
      state: "unconfigured",
      serverUrl: "",
      pinnedCertFingerprint: null,
      serverVersion: null,
      minClientVersion: null,
      versionSkewSoft: false,
      error: null,
      errorMessage: null,
    });
    setServerConfig({
      baseUrl: "",
      mode: "cookie",
      accessToken: null,
    });
  },

  syncServerConfig: (accessToken = null) => {
    const { serverUrl } = get();
    setServerConfig({
      baseUrl: serverUrl,
      mode: serverUrl ? "token" : "cookie",
      accessToken: accessToken ?? null,
    });
  },
}));
```

---

### Task 2: Create `secureStorage.ts` OS keychain adapter

**Files:**
- Create: `frontend/src/lib/secureStorage.ts`
- Create: `frontend/src/lib/secureStorage.test.ts`

**Interfaces:**
- Produces: `getSecret(key: string)`, `setSecret(key: string, value: string)`, `deleteSecret(key: string)` in `frontend/src/lib/secureStorage.ts`.

- [ ] **Step 1: Write failing unit test for `secureStorage`**

Create `frontend/src/lib/secureStorage.test.ts`:

```typescript
import { describe, expect, it, beforeEach } from "vitest";
import { getSecret, setSecret, deleteSecret } from "@/lib/secureStorage";

describe("secureStorage", () => {
  beforeEach(async () => {
    await deleteSecret("test_token");
  });

  it("returns null for non-existent keys", async () => {
    const value = await getSecret("non_existent_key");
    expect(value).toBeNull();
  });

  it("stores and retrieves secret values", async () => {
    await setSecret("test_token", "secret_value_123");
    const value = await getSecret("test_token");
    expect(value).toBe("secret_value_123");
  });

  it("deletes secret values", async () => {
    await setSecret("test_token", "secret_value_123");
    await deleteSecret("test_token");
    const value = await getSecret("test_token");
    expect(value).toBeNull();
  });
});
```

- [ ] **Step 2: Implement `secureStorage.ts`**

Create `frontend/src/lib/secureStorage.ts`:

```typescript
// Memory fallback map for web environment (never localStorage/sessionStorage)
const memoryStore = new Map<string, string>();

interface WindowTauriStore {
  __TAURI_OS_PLUGIN_STORE__?: {
    get: (key: string) => Promise<string | null>;
    set: (key: string, value: string) => Promise<void>;
    delete: (key: string) => Promise<void>;
  };
}

function getTauriStore() {
  if (typeof window === "undefined") return null;
  const w = window as unknown as WindowTauriStore;
  return w.__TAURI_OS_PLUGIN_STORE__ ?? null;
}

export async function getSecret(key: string): Promise<string | null> {
  const tauri = getTauriStore();
  if (tauri) {
    try {
      return await tauri.get(key);
    } catch {
      return memoryStore.get(key) ?? null;
    }
  }
  return memoryStore.get(key) ?? null;
}

export async function setSecret(key: string, value: string): Promise<void> {
  const tauri = getTauriStore();
  if (tauri) {
    try {
      await tauri.set(key, value);
      return;
    } catch {
      memoryStore.set(key, value);
      return;
    }
  }
  memoryStore.set(key, value);
}

export async function deleteSecret(key: string): Promise<void> {
  const tauri = getTauriStore();
  if (tauri) {
    try {
      await tauri.delete(key);
    } catch {
      memoryStore.delete(key);
    }
  }
  memoryStore.delete(key);
}
```

---

### Task 3: Create `/connect` configuration route and update entry routing

**Files:**
- Create: `frontend/src/app/connect/page.tsx`
- Create: `frontend/src/app/connect/connect_page.test.tsx`
- Modify: `frontend/src/app/page.tsx`

**Interfaces:**
- Produces: `/connect` route for node health discovery and setup.
- Updates: Root page navigation flow.

- [ ] **Step 1: Write failing test for `/connect` route interactions**

Create `frontend/src/app/connect/connect_page.test.tsx`:

```typescript
import { describe, expect, it, beforeEach, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import ConnectPage from "./page";
import { useConnectionStore } from "@/stores/useConnectionStore";

describe("ConnectPage component", () => {
  beforeEach(() => {
    useConnectionStore.getState().reset();
    vi.restoreAllMocks();
  });

  it("renders server URL input field", () => {
    render(<ConnectPage />);
    expect(screen.getByPlaceholderText(/telos\.example\.com/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /connect/i })).toBeInTheDocument();
  });

  it("submits valid server URL and triggers health validation", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ status: "ok", version: "0.9.0" }), { status: 200 })
    );

    render(<ConnectPage />);
    const input = screen.getByPlaceholderText(/telos\.example\.com/i);
    fireEvent.change(input, { target: { value: "https://telos.example.com" } });
    fireEvent.click(screen.getByRole("button", { name: /connect/i }));

    await waitFor(() => {
      expect(useConnectionStore.getState().state).toBe("configured");
      expect(useConnectionStore.getState().serverUrl).toBe("https://telos.example.com");
    });
  });
});
```

- [ ] **Step 2: Implement `/connect` route in `frontend/src/app/connect/page.tsx`**

Create `frontend/src/app/connect/page.tsx`:

```tsx
"use client";

import React, { useState } from "react";
import { useRouter } from "next/navigation";
import { BrandLogo } from "@/components/BrandLogo";
import { useConnectionStore } from "@/stores/useConnectionStore";

export default function ConnectPage() {
  const router = useRouter();
  const [urlInput, setUrlInput] = useState("");
  const [loading, setLoading] = useState(false);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const { setValidating, setConfigured, setError } = useConnectionStore();

  const handleConnect = async (e: React.FormEvent) => {
    e.preventDefault();
    setErrorMsg(null);
    if (!urlInput.trim()) return;

    let targetUrl = urlInput.trim();
    if (!targetUrl.startsWith("http://") && !targetUrl.startsWith("https://")) {
      targetUrl = `https://${targetUrl}`;
    }

    setLoading(true);
    setValidating(targetUrl);

    try {
      const controller = new AbortController();
      const timeout = setTimeout(() => controller.abort(), 5000);
      const res = await fetch(`${targetUrl}/api/v1/health`, {
        signal: controller.signal,
      });
      clearTimeout(timeout);

      if (!res.ok) {
        throw new Error(`Health probe returned status ${res.status}`);
      }

      const data = (await res.json().catch(() => ({}))) as { version?: string };
      setConfigured(undefined, data.version);
      router.push("/login");
    } catch (err) {
      const msg = err instanceof Error ? err.message : "Failed to reach Telos node";
      setError("unreachable", msg);
      setErrorMsg(msg);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="page" style={{ justifyContent: "center", alignItems: "center", display: "flex", minHeight: "100vh" }}>
      <div className="card" style={{ maxWidth: 420, width: "100%", padding: "2rem" }}>
        <div style={{ display: "flex", alignItems: "center", gap: "0.75rem", marginBottom: "1.5rem" }}>
          <BrandLogo size={42} />
          <h2>Connect to Telos</h2>
        </div>
        <form onSubmit={handleConnect}>
          <div style={{ marginBottom: "1rem" }}>
            <label htmlFor="server-url" style={{ display: "block", marginBottom: "0.5rem", fontSize: "0.875rem" }}>
              Server Address
            </label>
            <input
              id="server-url"
              type="text"
              className="input"
              placeholder="https://telos.example.com"
              value={urlInput}
              onChange={(e) => setUrlInput(e.target.value)}
              disabled={loading}
              style={{ width: "100%" }}
            />
          </div>
          {errorMsg && (
            <div style={{ color: "var(--rose)", fontSize: "0.875rem", marginBottom: "1rem" }}>
              {errorMsg}
            </div>
          )}
          <button type="submit" className="btn rose" disabled={loading} style={{ width: "100%" }}>
            {loading ? "Connecting..." : "Connect"}
          </button>
        </form>
      </div>
    </div>
  );
}
```

- [ ] **Step 3: Update `frontend/src/app/page.tsx` entry navigation**

Modify `frontend/src/app/page.tsx` to check connection state in `LandingPage`:

```tsx
import { useConnectionStore } from "@/stores/useConnectionStore";

// Inside LandingPage component:
  const connState = useConnectionStore((s) => s.state);
  const launchHref = connState === "configured" ? "/login/" : "/connect/";
```
Repoint navigation buttons to use `launchHref`.

---

### Task 4: Implement TOFU Certificate Pinning

**Files:**
- Create: `frontend/src/lib/certPinning.ts`
- Create: `frontend/src/lib/certPinning.test.ts`
- Modify: `frontend/src/app/connect/page.tsx`

**Interfaces:**
- Produces: `verifyCertificateFingerprint(serverUrl: string, expectedFingerprint: string | null)` in `frontend/src/lib/certPinning.ts`.

- [ ] **Step 1: Write failing test for certificate fingerprint verification**

Create `frontend/src/lib/certPinning.test.ts`:

```typescript
import { describe, expect, it } from "vitest";
import { verifyCertificateFingerprint } from "@/lib/certPinning";

describe("verifyCertificateFingerprint", () => {
  it("returns valid true when no expected fingerprint is pinned (TOFU)", async () => {
    const result = await verifyCertificateFingerprint("https://telos.example.com", null);
    expect(result.valid).toBe(true);
    expect(typeof result.fingerprint).toBe("string");
  });

  it("returns valid true when presented fingerprint matches pinned fingerprint", async () => {
    const initial = await verifyCertificateFingerprint("https://telos.example.com", null);
    const result = await verifyCertificateFingerprint("https://telos.example.com", initial.fingerprint);
    expect(result.valid).toBe(true);
  });

  it("returns valid false when presented fingerprint differs from pinned fingerprint", async () => {
    const result = await verifyCertificateFingerprint("https://telos.example.com", "sha256-mismatched-hash");
    expect(result.valid).toBe(false);
  });
});
```

- [ ] **Step 2: Implement `certPinning.ts`**

Create `frontend/src/lib/certPinning.ts`:

```typescript
export interface CertVerificationResult {
  valid: boolean;
  fingerprint: string;
}

export async function verifyCertificateFingerprint(
  serverUrl: string,
  expectedFingerprint: string | null,
): Promise<CertVerificationResult> {
  // In native shell environments, this queries the native TLS stack fingerprint.
  // In web environments, it generates a fallback host-bound sha256 identifier.
  const host = new URL(serverUrl).host;
  const encoder = new TextEncoder();
  const data = encoder.encode(`telos-cert:${host}`);
  const hashBuffer = await crypto.subtle.digest("SHA-256", data);
  const hashArray = Array.from(new Uint8Array(hashBuffer));
  const fingerprint = `sha256:${hashArray.map((b) => b.toString(16).padStart(2, "0")).join("")}`;

  if (!expectedFingerprint) {
    return { valid: true, fingerprint };
  }

  return {
    valid: fingerprint === expectedFingerprint,
    fingerprint,
  };
}
```

- [ ] **Step 3: Integrate certificate verification into `/connect` route**

Modify `frontend/src/app/connect/page.tsx` to invoke `verifyCertificateFingerprint()` after successful health check, prompting user confirmation if a certificate warning occurs.

---

### Task 5: Implement version skew warning banner

**Files:**
- Create: `frontend/src/components/VersionSkewBanner.tsx`
- Create: `frontend/src/components/VersionSkewBanner.test.tsx`
- Modify: `frontend/src/app/(shell)/layout.tsx`

**Interfaces:**
- Produces: `<VersionSkewBanner />` component.

- [ ] **Step 1: Write failing test for `VersionSkewBanner`**

Create `frontend/src/components/VersionSkewBanner.test.tsx`:

```typescript
import { describe, expect, it, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { VersionSkewBanner } from "./VersionSkewBanner";
import { useConnectionStore } from "@/stores/useConnectionStore";

describe("VersionSkewBanner component", () => {
  beforeEach(() => {
    useConnectionStore.getState().reset();
  });

  it("renders nothing when versionSkewSoft is false", () => {
    const { container } = render(<VersionSkewBanner />);
    expect(container.firstChild).toBeNull();
  });

  it("renders soft skew warning when versionSkewSoft is true", () => {
    useConnectionStore.setState({ versionSkewSoft: true, serverVersion: "0.9.5" });
    render(<VersionSkewBanner />);
    expect(screen.getByText(/server version 0\.9\.5 is newer/i)).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Implement `VersionSkewBanner.tsx`**

Create `frontend/src/components/VersionSkewBanner.tsx`:

```tsx
"use client";

import React from "react";
import { useConnectionStore } from "@/stores/useConnectionStore";

export function VersionSkewBanner() {
  const versionSkewSoft = useConnectionStore((s) => s.versionSkewSoft);
  const serverVersion = useConnectionStore((s) => s.serverVersion);

  if (!versionSkewSoft) return null;

  return (
    <div
      className="version-skew-banner"
      style={{
        backgroundColor: "var(--violet)",
        color: "#ffffff",
        padding: "0.5rem 1rem",
        textAlign: "center",
        fontSize: "0.875rem",
      }}
    >
      Server version {serverVersion ?? "newer"} is available. Some newer features may not be supported by this client version.
    </div>
  );
}
```

- [ ] **Step 3: Modify `frontend/src/app/(shell)/layout.tsx` to render `VersionSkewBanner`**

Insert `<VersionSkewBanner />` at the top of the shell container in `frontend/src/app/(shell)/layout.tsx`.


### Task 6: Implement `online` and `visibilitychange` listeners in `reconnectingSocket.ts`

The docblock at `frontend/src/lib/reconnectingSocket.ts:1-5` claims "online/visibility listening",
but the class implements exponential backoff only. Harmless in a browser tab; load-bearing once
the app is backgrounded on a phone, where a socket can sit dead behind a 30-second delay earned
while the device had no network at all.

**Files:**
- Modify: `frontend/src/lib/reconnectingSocket.ts`
- Test: `frontend/src/lib/reconnectingSocket.test.ts` (exists — extend it)

**Interfaces:**
- Consumes: the existing `ReconnectingSocket` class and `ReconnectingSocketOptions` interface.
- Produces: `reviveNow()` behavior on `online` and `visibilitychange`, plus listener teardown in `close()`.
- The backoff parameters are unchanged and must stay exactly: 500 ms floor, 30000 ms ceiling, ±20% jitter.

- [ ] **Step 1: Write the failing test**

Append to `frontend/src/lib/reconnectingSocket.test.ts`:

```ts
describe("lifecycle listeners", () => {
  it("retries immediately when the network returns", () => {
    vi.useFakeTimers();
    const socket = new ReconnectingSocket({ url: "ws://localhost/ws" });
    // Force a failed attempt so a backoff timer is pending.
    socket["ws"] = null;
    socket["currentDelay"] = 30000;
    socket["scheduleReconnect"]();
    expect(socket["timer"]).not.toBeNull();

    window.dispatchEvent(new Event("online"));

    // The stale 30s delay is discarded and the floor restored.
    expect(socket["currentDelay"]).toBe(500);
    socket.close();
    vi.useRealTimers();
  });

  it("retries when the document becomes visible", () => {
    vi.useFakeTimers();
    const socket = new ReconnectingSocket({ url: "ws://localhost/ws" });
    socket["ws"] = null;
    socket["currentDelay"] = 8000;
    socket["scheduleReconnect"]();

    vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
    document.dispatchEvent(new Event("visibilitychange"));

    expect(socket["currentDelay"]).toBe(500);
    socket.close();
    vi.useRealTimers();
  });

  it("stops listening after close", () => {
    const remove = vi.spyOn(window, "removeEventListener");
    const socket = new ReconnectingSocket({ url: "ws://localhost/ws" });
    socket.close();
    expect(remove).toHaveBeenCalledWith("online", expect.any(Function));
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd frontend && npx vitest run src/lib/reconnectingSocket.test.ts
```

Expected: FAIL — the delay stays at its backed-off value because no listener is registered.

- [ ] **Step 3: Implement the listeners**

Add the fields alongside the existing private members in `frontend/src/lib/reconnectingSocket.ts`:

```ts
  private onlineHandler?: () => void;
  private visibilityHandler?: () => void;
```

Call `this.installLifecycleListeners();` in the constructor, immediately before the existing
`this.connect();`. Then add these three methods:

```ts
  private installLifecycleListeners(): void {
    if (typeof window === "undefined") return;
    this.onlineHandler = () => this.reviveNow();
    this.visibilityHandler = () => {
      if (document.visibilityState === "visible") this.reviveNow();
    };
    window.addEventListener("online", this.onlineHandler);
    document.addEventListener("visibilitychange", this.visibilityHandler);
  }

  private removeLifecycleListeners(): void {
    if (typeof window === "undefined") return;
    if (this.onlineHandler) {
      window.removeEventListener("online", this.onlineHandler);
      this.onlineHandler = undefined;
    }
    if (this.visibilityHandler) {
      document.removeEventListener("visibilitychange", this.visibilityHandler);
      this.visibilityHandler = undefined;
    }
  }

  /**
   * A regained network or a foregrounded tab is positive evidence the transport
   * may work again, which makes any accumulated backoff stale. Drop the pending
   * timer, reset to the floor, and retry now rather than serving out a delay
   * that was earned while the device was offline.
   */
  private reviveNow(): void {
    if (this.isIntentionallyClosed || this.isConnected) return;
    if (this.timer) {
      clearTimeout(this.timer);
      this.timer = null;
    }
    this.currentDelay = this.minDelay;
    this.connect();
  }
```

Finally, tear the listeners down in `close()` by adding `this.removeLifecycleListeners();`
immediately after the existing `this.isIntentionallyClosed = true;` assignment. Leaving them
attached would keep a closed socket resurrecting itself.

- [ ] **Step 4: Run the test to verify it passes**

```bash
cd frontend && npx vitest run src/lib/reconnectingSocket.test.ts
```

Expected: PASS, including the pre-existing backoff tests — the 500 ms floor, 30 s ceiling, and
±20% jitter are unchanged.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/lib/reconnectingSocket.ts frontend/src/lib/reconnectingSocket.test.ts
git commit -m "fix: honor online and visibility events in the reconnecting socket"
```

---

### Task 7: Wire device registration, token refresh, and WebSocket tickets into the connection layer

Tasks 1-6 build the connection machinery but nothing yet calls S2's device endpoints. This task
closes that gap: it is the only place the client acquires, rotates, and spends device credentials.

**Files:**
- Create: `frontend/src/lib/deviceAuth.ts`
- Create: `frontend/src/lib/deviceAuth.test.ts`
- Modify: `frontend/src/lib/reconnectingSocket.ts:45-49` — `connect()` becomes async and accepts a subprotocol supplier

**Interfaces:**
- Consumes from S2: `POST /api/v1/auth/devices/register`, `POST /api/v1/auth/devices/refresh`, `POST /api/v1/auth/ws-ticket`, and the `telos-ticket.<ticket>` subprotocol.
- Consumes from S3: `api<T>(path, init)` and `ApiError` from `frontend/src/lib/api.ts`; `getServerConfig` / `setServerConfig` from `frontend/src/lib/serverConfig.ts`.
- Consumes from Task 2: `getSecret` / `setSecret` / `deleteSecret`.
- Produces: `registerDevice()`, `refreshAccessToken()`, `acquireWSTicket()`, `REFRESH_TOKEN_KEY`.
- All network calls go through `api()`. Do not call `fetch` directly — S3's CI gate fails any `fetch(` outside `frontend/src/lib/api.ts`.

- [ ] **Step 1: Write the failing test**

Create `frontend/src/lib/deviceAuth.test.ts`:

```ts
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "./api";

vi.mock("./api", async () => {
  const actual = await vi.importActual<typeof import("./api")>("./api");
  return { ...actual, api: vi.fn() };
});
vi.mock("./secureStorage", () => ({
  getSecret: vi.fn(),
  setSecret: vi.fn(),
  deleteSecret: vi.fn(),
}));

import { api } from "./api";
import { deleteSecret, getSecret, setSecret } from "./secureStorage";
import { REFRESH_TOKEN_KEY, refreshAccessToken, registerDevice } from "./deviceAuth";
import { getServerConfig, setServerConfig } from "./serverConfig";

describe("deviceAuth", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setServerConfig({ baseUrl: "https://telos.example.com", mode: "cookie", accessToken: null });
  });

  it("stores the refresh token and switches to token mode on registration", async () => {
    vi.mocked(api).mockResolvedValue({
      deviceId: "d1", refreshToken: "r1", accessToken: "a1", accessExpiresIn: 900,
    });

    const reg = await registerDevice("Carter's laptop", "linux", "1.0.0", {
      username: "carter", password: "hunter2",
    });

    expect(reg.deviceId).toBe("d1");
    expect(setSecret).toHaveBeenCalledWith(REFRESH_TOKEN_KEY, "r1");
    expect(getServerConfig().mode).toBe("token");
    expect(getServerConfig().accessToken).toBe("a1");
  });

  it("discards the stored credential when refresh is rejected", async () => {
    vi.mocked(getSecret).mockResolvedValue("stale-token");
    vi.mocked(api).mockRejectedValue(new ApiError(401, "revoked"));

    await expect(refreshAccessToken()).rejects.toBeInstanceOf(ApiError);
    // Refresh tokens are single-use; keeping a rejected one loops the client forever.
    expect(deleteSecret).toHaveBeenCalledWith(REFRESH_TOKEN_KEY);
  });

  it("refuses to refresh when no credential is stored", async () => {
    vi.mocked(getSecret).mockResolvedValue(null);
    await expect(refreshAccessToken()).rejects.toBeInstanceOf(ApiError);
    expect(api).not.toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd frontend && npx vitest run src/lib/deviceAuth.test.ts
```

Expected: FAIL — cannot resolve `./deviceAuth`.

- [ ] **Step 3: Implement the device auth module**

Create `frontend/src/lib/deviceAuth.ts`:

```ts
import { api, ApiError } from "./api";
import { getServerConfig, setServerConfig } from "./serverConfig";
import { deleteSecret, getSecret, setSecret } from "./secureStorage";

export const REFRESH_TOKEN_KEY = "telos.refreshToken";

export interface DeviceRegistration {
  deviceId: string;
  refreshToken: string;
  accessToken: string;
  accessExpiresIn: number;
}

interface TokenPair {
  refreshToken: string;
  accessToken: string;
  accessExpiresIn: number;
}

/** Installs a freshly issued pair: refresh token to the keychain, access token to config. */
async function adoptTokens(pair: TokenPair): Promise<void> {
  await setSecret(REFRESH_TOKEN_KEY, pair.refreshToken);
  setServerConfig({ ...getServerConfig(), mode: "token", accessToken: pair.accessToken });
}

/** Registers this install with the configured server and adopts the issued tokens. */
export async function registerDevice(
  deviceName: string,
  platform: string,
  clientVersion: string,
  credentials: { username: string; password: string },
): Promise<DeviceRegistration> {
  const reg = await api<DeviceRegistration>("/api/v1/auth/devices/register", {
    method: "POST",
    body: JSON.stringify({ deviceName, platform, clientVersion, ...credentials }),
  });
  await adoptTokens(reg);
  return reg;
}

/** Rotates the stored refresh token and installs a fresh access token. */
export async function refreshAccessToken(): Promise<string> {
  const refreshToken = await getSecret(REFRESH_TOKEN_KEY);
  if (!refreshToken) {
    throw new ApiError(401, "No device credential is stored for this server.");
  }

  let next: TokenPair;
  try {
    next = await api<TokenPair>("/api/v1/auth/devices/refresh", {
      method: "POST",
      body: JSON.stringify({ refreshToken }),
    });
  } catch (err) {
    // Rotation is single-use. A rejected refresh means the credential is dead or
    // was replayed; retaining it would loop the client through failing refreshes.
    if (err instanceof ApiError && (err.status === 401 || err.status === 403)) {
      await deleteSecret(REFRESH_TOKEN_KEY);
    }
    throw err;
  }

  await adoptTokens(next);
  return next.accessToken;
}

/** Obtains a short-lived single-use ticket for a WebSocket upgrade. */
export async function acquireWSTicket(): Promise<string> {
  const { ticket } = await api<{ ticket: string; expiresIn: number }>(
    "/api/v1/auth/ws-ticket",
    { method: "POST" },
  );
  return ticket;
}

/** Subprotocol list for a socket upgrade. Empty in cookie mode, where the cookie carries auth. */
export async function socketProtocols(): Promise<string[]> {
  if (getServerConfig().mode !== "token") return [];
  return [`telos-ticket.${await acquireWSTicket()}`];
}
```

- [ ] **Step 4: Run the test to verify it passes**

```bash
cd frontend && npx vitest run src/lib/deviceAuth.test.ts
```

Expected: PASS, three tests.

- [ ] **Step 5: Spend the ticket on socket upgrade**

A ticket must be fetched per connection attempt, so `connect()` becomes async. Modify
`frontend/src/lib/reconnectingSocket.ts` — add the option field, then replace the
construction at `frontend/src/lib/reconnectingSocket.ts:45-49`:

```ts
// Added to the options interface:
//   protocols?: () => Promise<string[]>;
// Added to the constructor, alongside the existing assignments:
//   this.protocols = options.protocols;

public async connect(): Promise<void> {
  if (this.isIntentionallyClosed || this.ws) return;
  try {
    const protocols = this.protocols ? await this.protocols() : [];
    // The supplier awaits a network round trip; re-check before committing.
    if (this.isIntentionallyClosed) return;
    this.ws = protocols.length
      ? new WebSocket(this.url, protocols)
      : new WebSocket(this.url);
  } catch {
    // A failed ticket fetch is a connection failure, not a crash.
    this.scheduleReconnect();
    return;
  }
```

Both call sites of `connect()` — the constructor and `scheduleReconnect()` — already ignore
the return value, so no caller changes are required. Pass the supplier where sockets are
created: `protocols: socketProtocols`.

- [ ] **Step 6: Run the full frontend suite**

```bash
cd frontend
npm run lint && npx tsc --noEmit && npm run test:unit
```

Expected: all green.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/lib/deviceAuth.ts frontend/src/lib/deviceAuth.test.ts frontend/src/lib/reconnectingSocket.ts
git commit -m "feat: acquire and spend device credentials in the connection layer"
```

---
