import type { NextConfig } from "next";

const rawDevOrigins = (process.env.TELOS_DEV_ORIGINS ?? "")
  .split(",")
  .map((origin) => origin.trim())
  .filter(Boolean);

const configuredDevOrigins = Array.from(
  new Set(
    rawDevOrigins.flatMap((o) => {
      const stripped = o.replace(/^https?:\/\//, "");
      const hostOnly = stripped.split(":")[0];
      return [o, stripped, hostOnly];
    }),
  ),
).filter(Boolean);

const nextConfig: NextConfig = {
  // Next blocks dev-only assets (including the HMR socket that enables React
  // hydration) when the browser reaches :3000 through a different hostname.
  // Configure comma-separated LAN/Tailscale hostnames with
  // TELOS_DEV_ORIGINS in .env.development.local or the launch environment.
  allowedDevOrigins: configuredDevOrigins,
  output: "export",
  // Stable build ID so two builds of unchanged source produce an identical
  // embedded frontend inventory (reproducible-build gate).
  generateBuildId: () => "telos-static",
  // Served by the Go gateway's embedded file server; keep URLs directory-style
  // so /chat resolves to /chat/index.html.
  trailingSlash: true,
  images: { unoptimized: true },
};

export default nextConfig;
