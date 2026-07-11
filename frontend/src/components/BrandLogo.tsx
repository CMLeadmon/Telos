"use client";

import { useThemeStore } from "@/stores/useThemeStore";

// The vaporwave lockup belongs to synthwave; the ink logos to Standard.
export function BrandLogo({ size = 46 }: { size?: number }) {
  const theme = useThemeStore((s) => s.theme);
  const src = theme === "ink" ? "/logos/telos-ink.png" : "/logos/telos-vaporwave.png";
  // eslint-disable-next-line @next/next/no-img-element
  return <img src={src} alt="Telos" style={{ height: size, width: size }} />;
}
