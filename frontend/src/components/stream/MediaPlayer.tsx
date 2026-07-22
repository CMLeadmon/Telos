"use client";

import { useEffect, useRef } from "react";
import type { PlaybackAdapter, WatchPartyState } from "@/lib/playbackSync";
import { projectPosition, driftCorrection } from "@/lib/playbackSync";

// MediaPlayer wraps a media element behind a PlaybackAdapter. In a party, only
// the host has native controls; participants' controls are disabled with a
// clear label and their element is driven toward the projected host position
// (drift corrected over the resync window).
export function MediaPlayer({
  src,
  isHost,
  partyState,
  onAdapter,
}: {
  src: string;
  isHost: boolean;
  partyState: WatchPartyState | null;
  onAdapter?: (a: PlaybackAdapter) => void;
}) {
  const videoRef = useRef<HTMLVideoElement>(null);

  useEffect(() => {
    const v = videoRef.current;
    if (!v || !onAdapter) return;
    const adapter: PlaybackAdapter = {
      snapshot: () => ({ positionSeconds: v.currentTime, paused: v.paused, rate: v.playbackRate }),
      apply: (t) => {
        if (Math.abs(v.currentTime - t.positionSeconds) > 0.25) v.currentTime = t.positionSeconds;
        v.playbackRate = t.rate;
        if (t.paused && !v.paused) v.pause();
        if (!t.paused && v.paused) void v.play().catch(() => {});
      },
      setControlsEnabled: (enabled) => {
        v.controls = enabled;
      },
    };
    onAdapter(adapter);
  }, [onAdapter]);

  // Participants follow the host-authoritative state; the host drives directly.
  useEffect(() => {
    const v = videoRef.current;
    if (!v || isHost || !partyState) return;
    const hostPos = projectPosition(partyState, Date.now());
    const correction = driftCorrection(v.currentTime, hostPos);
    if (correction !== null) v.currentTime = correction;
    v.playbackRate = partyState.playbackRate || 1;
    if (partyState.action === "paused" && !v.paused) v.pause();
    if (partyState.action === "playing" && v.paused) void v.play().catch(() => {});
  }, [isHost, partyState]);

  return (
    <div className="wp-player" data-testid="wp-media-player">
      <video
        ref={videoRef}
        src={src}
        controls={isHost}
        data-testid="wp-video"
        playsInline
      />
      {!isHost && partyState && (
        <p className="wp-follow-note" data-testid="wp-follow-note">
          Synchronized with the host — playback controls are disabled.
        </p>
      )}
    </div>
  );
}
