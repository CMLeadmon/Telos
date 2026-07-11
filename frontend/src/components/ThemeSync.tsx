"use client";

import { useEffect } from "react";
import { useThemeStore } from "@/stores/useThemeStore";

// Mirrors the persisted theme onto <html data-theme> — the DS is themed
// entirely by that attribute (token swap only, never a layout change).
export function ThemeSync() {
  const theme = useThemeStore((s) => s.theme);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);

  return null;
}
