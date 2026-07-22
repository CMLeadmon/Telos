"use client";

import { useState } from "react";
import { useWatchPartyStore } from "@/stores/useWatchPartyStore";

// CreateWatchPartyDialog starts a party for a media item with optional linked
// text/voice channels. It preserves the draft on failure.
export function CreateWatchPartyDialog({
  mediaItemId,
  onClose,
}: {
  mediaItemId: string;
  onClose: () => void;
}) {
  const create = useWatchPartyStore((s) => s.create);
  const [textChannelId, setTextChannelId] = useState("");
  const [voiceChannelId, setVoiceChannelId] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async () => {
    setBusy(true);
    setError(null);
    try {
      await create(mediaItemId, textChannelId || undefined, voiceChannelId || undefined);
      onClose();
    } catch {
      setError("Could not start the party — your choices were kept.");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="wp-dialog" role="dialog" aria-label="Start a Watch Party" data-testid="create-watch-party">
      <h3>Start a Watch Party</h3>
      <label>
        Text channel (optional)
        <input value={textChannelId} onChange={(e) => setTextChannelId(e.target.value)} placeholder="channel id" />
      </label>
      <label>
        Voice channel (optional)
        <input value={voiceChannelId} onChange={(e) => setVoiceChannelId(e.target.value)} placeholder="channel id" />
      </label>
      {error && <p className="wp-error" role="alert">{error}</p>}
      <div className="wp-actions">
        <button onClick={() => void submit()} disabled={busy} data-testid="create-party-submit">
          {busy ? "Starting…" : "Start party"}
        </button>
        <button className="wp-cancel" onClick={onClose}>
          Cancel
        </button>
      </div>
    </div>
  );
}
