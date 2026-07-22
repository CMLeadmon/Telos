// Pure playback-synchronization helpers for host-authoritative Watch Parties.

export interface WatchPartyState {
  version: number;
  action: "playing" | "paused";
  positionSeconds: number;
  playbackRate: number;
  mediaItemId: string;
  serverTime: string; // RFC3339
  leaseExpiresAt: string; // RFC3339
}

// PlaybackAdapter abstracts the underlying media element so the store can drive
// any player (HTML5 video, HLS.js) and disable controls for participants.
export interface PlaybackAdapter {
  snapshot: () => { positionSeconds: number; paused: boolean; rate: number };
  apply: (target: { positionSeconds: number; paused: boolean; rate: number }) => void;
  setControlsEnabled: (enabled: boolean) => void;
}

// projectPosition derives the host's current position from the authoritative
// state and the current wall-clock, accounting for elapsed time and rate while
// playing. Paused state holds its position.
export function projectPosition(state: WatchPartyState, nowMs: number): number {
  if (state.action !== "playing") return state.positionSeconds;
  const serverMs = Date.parse(state.serverTime);
  if (!Number.isFinite(serverMs)) return state.positionSeconds;
  const elapsed = Math.max(0, (nowMs - serverMs) / 1000);
  return state.positionSeconds + elapsed * (state.playbackRate || 1);
}

// isNewerVersion reports whether incoming supersedes current; equal or older
// versions are stale and must be ignored.
export function isNewerVersion(current: number, incoming: number): boolean {
  return incoming > current;
}

export const DRIFT_THRESHOLD_SECONDS = 1.5;
export const RESYNC_WINDOW_SECONDS = 2;

// driftCorrection returns the position a participant should ease toward when its
// local position has drifted from the projected host position beyond the
// threshold; null means no correction is needed.
export function driftCorrection(localPos: number, hostPos: number): number | null {
  if (Math.abs(localPos - hostPos) <= DRIFT_THRESHOLD_SECONDS) return null;
  return hostPos;
}

// leaseSecondsRemaining returns whole seconds until the host lease expires, or 0
// when already expired.
export function leaseSecondsRemaining(state: WatchPartyState, nowMs: number): number {
  const exp = Date.parse(state.leaseExpiresAt);
  if (!Number.isFinite(exp)) return 0;
  return Math.max(0, Math.floor((exp - nowMs) / 1000));
}
