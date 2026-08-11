import { useRef, useCallback } from "react";

export interface MediaProgressOptions {
  itemId: string;
  durationMs?: number;
  onSaveProgress: (locator: Record<string, unknown>, percent: number, completed: boolean) => Promise<void>;
  intervalMs?: number;
  /**
   * Maps a reported position onto the whole item's timeline. A multi-track
   * audiobook reports position within the current track while `durationMs` is
   * the whole book, so the two are only comparable after this mapping. The
   * locator still carries the raw per-track position, which is what the
   * gateway's audiobook locator contract expects. Defaults to identity for
   * single-timeline media.
   */
  toAbsolutePositionMs?: (positionMs: number) => number;
}

export function useMediaProgress({
  durationMs = 0,
  onSaveProgress,
  intervalMs = 15000,
  toAbsolutePositionMs,
}: MediaProgressOptions) {
  const lastSavedRef = useRef<{ positionMs: number; percent: number; time: number }>({
    positionMs: 0,
    percent: 0,
    time: 0,
  });

  const saveCurrentProgress = useCallback(
    async (positionMs: number, locatorExtra: Record<string, unknown> = {}, isCompleted = false) => {
      const absolutePositionMs = toAbsolutePositionMs
        ? toAbsolutePositionMs(positionMs)
        : positionMs;
      const total = durationMs > 0 ? durationMs : absolutePositionMs;
      const percent = total > 0 ? Math.min(1.0, absolutePositionMs / total) : 0;
      // Only an `ended` event or an explicit completion action completes an
      // item. Percentage alone must not — a member who scrubs near the end and
      // stops has not finished, and a silently completed item drops out of
      // Continue with no way back.
      const completed = isCompleted;

      const locator = {
        positionMs: Math.round(positionMs),
        ...locatorExtra,
      };

      lastSavedRef.current = {
        positionMs: Math.round(positionMs),
        percent,
        time: Date.now(),
      };

      try {
        await onSaveProgress(locator, percent, completed);
      } catch (err) {
        console.error("Failed to save media progress:", err);
      }
    },
    [durationMs, onSaveProgress, toAbsolutePositionMs]
  );

  const handleTimeUpdate = useCallback(
    (currentPositionMs: number, locatorExtra: Record<string, unknown> = {}) => {
      const now = Date.now();
      if (now - lastSavedRef.current.time >= intervalMs) {
        saveCurrentProgress(currentPositionMs, locatorExtra);
      }
    },
    [intervalMs, saveCurrentProgress]
  );

  const handlePause = useCallback(
    (currentPositionMs: number, locatorExtra: Record<string, unknown> = {}) => {
      saveCurrentProgress(currentPositionMs, locatorExtra);
    },
    [saveCurrentProgress]
  );

  const handleEnded = useCallback(
    (currentPositionMs: number, locatorExtra: Record<string, unknown> = {}) => {
      saveCurrentProgress(currentPositionMs, locatorExtra, true);
    },
    [saveCurrentProgress]
  );

  return {
    saveCurrentProgress,
    handleTimeUpdate,
    handlePause,
    handleEnded,
  };
}
