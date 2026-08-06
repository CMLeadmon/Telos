"use client";

import { useThemeStore } from "@/stores/useThemeStore";

// The vaporwave lockup belongs to synthwave; the ink logos to Standard.
// 40px matches the mockups, which override the DS shell's 46px inline. The
// mark is a full-bleed circle at the DS viewBox, so it needs no corner radius.
export function BrandLogo({ size = 40 }: { size?: number }) {
  const theme = useThemeStore((s) => s.theme);
  const src = theme === "ink" ? "/logos/telos-ink.svg" : "/logos/telos-vaporwave.svg";
  const style: React.CSSProperties = {
    height: size,
    width: size,
    objectFit: "contain",
  };
  // eslint-disable-next-line @next/next/no-img-element
  return <img src={src} alt="Telos" style={style} />;
}
