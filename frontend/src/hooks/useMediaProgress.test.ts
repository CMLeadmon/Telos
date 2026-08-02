import { renderHook, act } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { useMediaProgress } from "./useMediaProgress";

function setup(durationMs: number, intervalMs = 15000) {
  const onSaveProgress = vi.fn().mockResolvedValue(undefined);
  const { result } = renderHook(() =>
    useMediaProgress({ itemId: "item-1", durationMs, onSaveProgress, intervalMs }),
  );
  return { result, onSaveProgress };
}

describe("useMediaProgress", () => {
  // The design is explicit: an `ended` event or an explicit completion action
  // completes an item, percentage alone does not. A member who scrubs to the
  // last minute and stops has not finished, and a silently completed item
  // drops out of Continue with no way back.
  it("does not complete an item on percentage alone", async () => {
    const { result, onSaveProgress } = setup(1000);

    await act(async () => {
      await result.current.saveCurrentProgress(999);
    });

    expect(onSaveProgress).toHaveBeenCalledTimes(1);
    const [, percent, completed] = onSaveProgress.mock.calls[0];
    expect(percent).toBeGreaterThan(0.99);
    expect(completed).toBe(false);
  });

  it("completes on ended", async () => {
    const { result, onSaveProgress } = setup(1000);

    await act(async () => {
      result.current.handleEnded(1000);
    });

    expect(onSaveProgress.mock.calls[0][2]).toBe(true);
  });

  it("throttles periodic saves but never throttles a pause", async () => {
    const { result, onSaveProgress } = setup(600000);

    await act(async () => {
      // The first tick saves and starts the window.
      result.current.handleTimeUpdate(1000);
      result.current.handleTimeUpdate(2000);
      result.current.handleTimeUpdate(3000);
    });
    expect(onSaveProgress).toHaveBeenCalledTimes(1);

    // A pause is a real stopping point and must land immediately.
    await act(async () => {
      result.current.handlePause(4000);
    });
    expect(onSaveProgress).toHaveBeenCalledTimes(2);
  });

  it("carries the caller's locator fields through", async () => {
    const { result, onSaveProgress } = setup(60000);

    await act(async () => {
      result.current.handlePause(12000, { trackIndex: 3 });
    });

    expect(onSaveProgress.mock.calls[0][0]).toEqual({
      positionMs: 12000,
      trackIndex: 3,
    });
  });
});
