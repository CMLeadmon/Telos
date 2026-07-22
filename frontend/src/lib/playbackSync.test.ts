import { describe, expect, it } from "vitest";
import {
  projectPosition,
  isNewerVersion,
  driftCorrection,
  leaseSecondsRemaining,
  type WatchPartyState,
} from "./playbackSync";

function state(over: Partial<WatchPartyState> = {}): WatchPartyState {
  return {
    version: 1,
    action: "playing",
    positionSeconds: 100,
    playbackRate: 1,
    mediaItemId: "m",
    serverTime: "2026-07-20T00:00:00.000Z",
    leaseExpiresAt: "2026-07-20T00:00:15.000Z",
    ...over,
  };
}

describe("playbackSync", () => {
  it("projects the playing position from server time and rate", () => {
    const s = state({ positionSeconds: 100, playbackRate: 1 });
    const now = Date.parse("2026-07-20T00:00:05.000Z");
    expect(projectPosition(s, now)).toBeCloseTo(105);
    // At 2x rate, 5 real seconds advances 10 media seconds.
    expect(projectPosition(state({ playbackRate: 2 }), now)).toBeCloseTo(110);
  });

  it("holds position while paused regardless of elapsed time", () => {
    const s = state({ action: "paused", positionSeconds: 42 });
    expect(projectPosition(s, Date.parse("2026-07-20T00:05:00.000Z"))).toBe(42);
  });

  it("ignores stale or equal versions", () => {
    expect(isNewerVersion(5, 6)).toBe(true);
    expect(isNewerVersion(5, 5)).toBe(false);
    expect(isNewerVersion(5, 4)).toBe(false);
  });

  it("corrects drift only beyond the threshold", () => {
    expect(driftCorrection(100, 100.5)).toBeNull(); // within threshold
    expect(driftCorrection(100, 105)).toBe(105); // needs a jump
  });

  it("reports whole seconds remaining on the lease", () => {
    const s = state();
    expect(leaseSecondsRemaining(s, Date.parse("2026-07-20T00:00:05.000Z"))).toBe(10);
    expect(leaseSecondsRemaining(s, Date.parse("2026-07-20T00:00:20.000Z"))).toBe(0);
  });
});
