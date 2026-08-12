// What the surrounding platform can do, and how to ask it.
//
// One static export runs in two places: a browser served by the Go gateway, and
// a native webview loading from disk. The differences are small in number and
// large in consequence, and every one of them is here rather than spread across
// components.
//
// Nothing in this module imports a Tauri package. The shell publishes plain
// objects on `window` (see frontend/src-tauri/src/lib.rs) and this file feature-
// detects them, so the web build carries no native code it can never run.

import { apiBlob, assetUrl } from "./api";
import { getServerConfig } from "./serverConfig";
import { isMobileNative, isNativeApp } from "./nativeEnv";

// Re-exported so callers have one import for "what can this platform do".
export { isMobileNative, isNativeApp };

/** The shell's bridge for things a webview cannot do for itself. */
export interface NativeShellBridge {
  openExternal(url: string): Promise<void>;
  writeClipboard(text: string): Promise<void>;
}

interface NativeWindow {
  __TELOS_NATIVE_SHELL__?: NativeShellBridge;
}

function w(): NativeWindow | null {
  return typeof window === "undefined" ? null : (window as unknown as NativeWindow);
}

function shell(): NativeShellBridge | null {
  return w()?.__TELOS_NATIVE_SHELL__ ?? null;
}

/**
 * Hands a URL to the operating system's browser.
 *
 * In a browser this is an ordinary new tab. In the native client it has to
 * leave the app: a plain link navigates the single app window away from Telos,
 * and a webview has no back button, so the member ends up stranded on someone
 * else's page with no way home.
 */
export async function openExternal(url: string): Promise<void> {
  const bridge = shell();
  if (bridge) {
    try {
      await bridge.openExternal(url);
      return;
    } catch {
      // Fall through: a browser tab is better than nothing happening at all.
    }
  }
  window.open(url, "_blank", "noopener,noreferrer");
}

/**
 * Copies text, reporting whether it actually happened.
 *
 * navigator.clipboard exists only in a secure context, and a custom-scheme
 * webview is not always one. A copy that silently does nothing is worse than a
 * failed one — the member walks away believing they hold an invite token — so
 * the result is returned rather than swallowed.
 */
export async function writeClipboard(text: string): Promise<boolean> {
  const bridge = shell();
  if (bridge) {
    try {
      await bridge.writeClipboard(text);
      return true;
    } catch {
      // Fall through to the web API.
    }
  }
  if (typeof navigator !== "undefined" && navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      return false;
    }
  }
  return false;
}

function filenameFrom(path: string, fallback: string): string {
  const clean = path.split(/[?#]/)[0].replace(/\/+$/, "");
  const last = clean.slice(clean.lastIndexOf("/") + 1);
  return last && last !== "download" ? decodeURIComponent(last) : fallback;
}

/**
 * Opens a resource that lives on the Telos node — a file download, a book's
 * bytes — as opposed to somewhere on the web.
 *
 * These were `window.open` on the resource URL. That works in a browser only
 * because the browser attaches the session cookie to a navigation it starts. A
 * token-mode client has no cookie and cannot put an Authorization header on a
 * navigation, so the same call lands on a bare 401; in the native client, on a
 * 401 inside the app window with no way back. So in token mode the bytes are
 * pulled through the authenticated client and handed to the platform as a blob.
 */
export async function openNodeResource(
  path: string,
  suggestedName = "download",
): Promise<void> {
  if (getServerConfig().mode !== "token") {
    // assetUrl, not the bare path: a relative URL in the native client resolves
    // against the app bundle rather than the node.
    window.open(assetUrl(path), "_blank", "noopener,noreferrer");
    return;
  }

  const blob = await apiBlob(path);
  const objectUrl = URL.createObjectURL(blob);
  try {
    const a = document.createElement("a");
    a.href = objectUrl;
    a.download = filenameFrom(path, suggestedName);
    a.rel = "noopener";
    document.body.appendChild(a);
    a.click();
    a.remove();
  } finally {
    // Revoked on a delay: the click hands the URL to the platform's download
    // machinery, which has not necessarily read it by the time this returns.
    setTimeout(() => URL.revokeObjectURL(objectUrl), 60_000);
  }
}
