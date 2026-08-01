import { useRef, useCallback, useEffect } from "react";

export interface MediaProgressOptions {
  itemId: string;
  durationMs?: number;
  onSaveProgress: (locator: Record<string, unknown>, percent: number, completed: boolean) => Promise<void>;
  intervalMs?: number;
}

export function useMediaProgress({
  itemId: _itemId,
  durationMs = 0,
  onSaveProgress,
  intervalMs = 15000,
}: MediaProgressOptions) {
  const lastSavedRef = useRef<{ positionMs: number; percent: number; time: number }>({
    positionMs: 0,
    percent: 0,
    time: 0,
  });

  const timerRef = useRef<NodeJS.Timeout | null>(null);

  const saveCurrentProgress = useCallback(
    async (positionMs: number, locatorExtra: Record<string, unknown> = {}, isCompleted = false) => {
      const total = durationMs > 0 ? durationMs : positionMs;
      const percent = total > 0 ? Math.min(1.0, positionMs / total) : 0;
      const completed = isCompleted || percent >= 0.99;

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
    [durationMs, onSaveProgress]
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

  useEffect(() => {
    const timer = timerRef.current;
    return () => {
      if (timer) {
        clearInterval(timer);
      }
    };
  }, []);

  return {
    saveCurrentProgress,
    handleTimeUpdate,
    handlePause,
    handleEnded,
  };
}
