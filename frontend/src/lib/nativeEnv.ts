// Am I the downloadable client, or a browser tab?
//
// A leaf module with no imports on purpose. Both serverConfig.ts and platform.ts
// need this answer, and platform.ts reaches the network through api.ts, which
// reads serverConfig.ts — so putting the test in either of them closes an import
// cycle. Two copies would be worse: they would drift the moment the shell gained
// or renamed a bridge, and one caller would treat the same build as native while
// the other treated it as web.

interface NativeWindow {
  __TAURI_INTERNALS__?: unknown;
  __TELOS_NATIVE_SHELL__?: unknown;
  __TELOS_NATIVE_TLS__?: unknown;
  __TELOS_NATIVE_STORE__?: unknown;
}

/**
 * Detected by the globals the shell injects, never by URL scheme: Tauri v2
 * serves over http://tauri.localhost on Windows and tauri://localhost
 * elsewhere, so a scheme test is wrong on at least one platform.
 */
export function isNativeApp(): boolean {
  if (typeof window === "undefined") return false;
  const win = window as unknown as NativeWindow;
  return (
    "__TAURI_INTERNALS__" in win ||
    "__TELOS_NATIVE_SHELL__" in win ||
    "__TELOS_NATIVE_TLS__" in win ||
    "__TELOS_NATIVE_STORE__" in win
  );
}

/** A native build on a touch platform, where file and share affordances differ. */
export function isMobileNative(): boolean {
  if (!isNativeApp()) return false;
  if (typeof navigator === "undefined") return false;
  return /iphone|ipad|ipod|android/i.test(navigator.userAgent);
}
