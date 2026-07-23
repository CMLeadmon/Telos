"use client";

import { AlertTriangle, RefreshCw } from "lucide-react";
import { useAuthStore } from "@/stores/useAuthStore";

export function ConnectivityBanner() {
  const { connectivity, fetchMe } = useAuthStore();

  if (connectivity === "online") return null;

  return (
    <div
      className="connectivity-banner"
      data-testid="connectivity-banner"
      role="status"
      aria-live="polite"
    >
      <AlertTriangle size={16} />
      <span>
        {connectivity === "reconnecting"
          ? "Reconnecting to Telos node…"
          : "Network connectivity issue — retrying background connection."}
      </span>
      <button
        className="btn-ghost btn-sm"
        onClick={() => void fetchMe()}
        aria-label="Retry connection"
      >
        <RefreshCw size={14} /> Retry
      </button>
    </div>
  );
}
