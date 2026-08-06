"use client";

import { useEffect } from "react";
import { apiBase } from "@/lib/api";
import { useThemeStore } from "@/stores/useThemeStore";

// Mirrors the persisted theme onto <html data-theme> — the DS is themed
// entirely by that attribute (token swap only, never a layout change).
//
// The same value is mirrored into a cookie the gateway reads, so the served
// document already carries the right data-theme and there is no flash of the
// default theme before hydration. The cookie is a paint-time hint only: server
// preferences remain the source of truth and still correct the client on load.
export function ThemeSync() {
  const theme = useThemeStore((s) => s.theme);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;

    // The cookie exists only so the gateway can stamp data-theme into the
    // document it serves. An empty apiBase() means we are same-origin with
    // that gateway. Once the client points at a remote server — a native
    // shell, or any cross-origin build — nothing reads this cookie, and
    // writing it would leave a preference on an origin that never asked for
    // it. Keying on apiBase() rather than the protocol keeps that true
    // automatically: native shells whose scheme still looks like http (for
    // example http://tauri.localhost on Android) are covered too.
    if (apiBase() === "") {
      const secure = window.location.protocol === "https:" ? "; Secure" : "";
      const oneYear = 60 * 60 * 24 * 365;
      document.cookie = `telos_theme=${theme}; Path=/; Max-Age=${oneYear}; SameSite=Lax${secure}`;
    }
  }, [theme]);

  return null;
}
