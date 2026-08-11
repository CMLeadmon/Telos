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
