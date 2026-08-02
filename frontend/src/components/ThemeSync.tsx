"use client";

import { useEffect } from "react";
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

    // Secure only over HTTPS, mirroring the gateway's dev/production split —
    // a Secure cookie on plain-http dev would simply never be stored.
    const secure = window.location.protocol === "https:" ? "; Secure" : "";
    const oneYear = 60 * 60 * 24 * 365;
    document.cookie = `telos_theme=${theme}; Path=/; Max-Age=${oneYear}; SameSite=Lax${secure}`;
  }, [theme]);

  return null;
}
