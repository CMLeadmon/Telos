import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  output: "export",
  // Served by the Go gateway's embedded file server; keep URLs directory-style
  // so /chat resolves to /chat/index.html.
  trailingSlash: true,
  images: { unoptimized: true },
};

export default nextConfig;
