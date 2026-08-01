import React, { useEffect, useRef, useState } from "react";
import { useMediaProgress } from "../../hooks/useMediaProgress";
import { api } from "../../lib/api";

export interface AudiobookTrack {
  index: number;
  title: string;
  durationMs: number;
  cumulativeStartMs: number;
}

export interface AudiobookChapter {
  index: number;
  title: string;
  startTimeMs: number;
  endTimeMs: number;
  durationMs: number;
}

export interface AudiobookInfo {
  id: string;
  title: string;
  author: string;
  narrator: string;
  durationMs: number;
  codec: string;
  tracks: AudiobookTrack[];
  chapters: AudiobookChapter[];
  progress?: {
    locator?: {
      positionMs?: number;
      trackIndex?: number;
    };
    percent?: number;
    completed?: boolean;
  };
}

interface AudiobookPlayerProps {
  itemId: string;
  onClose: () => void;
}

export function AudiobookPlayer({ itemId, onClose }: AudiobookPlayerProps) {
  const audioRef = useRef<HTMLAudioElement>(null);
  const [info, setInfo] = useState<AudiobookInfo | null>(null);
  const [isPlaying, setIsPlaying] = useState(false);
  const [currentTrackIndex, setCurrentTrackIndex] = useState(0);
  const [currentTimeSec, setCurrentTimeSec] = useState(0);
  const [durationSec, setDurationSec] = useState(0);
  const [playbackRate, setPlaybackRate] = useState(1.0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    async function loadInfo() {
      try {
        setLoading(true);
        const res = await api.get<AudiobookInfo>(`/api/v1/library/audiobooks/${itemId}/info`);
        if (active && res) {
          setInfo(res);
          if (res.progress?.locator?.trackIndex !== undefined) {
            setCurrentTrackIndex(res.progress.locator.trackIndex);
          }
        }
      } catch (err: unknown) {
        if (active) setError(err instanceof Error ? err.message : "Failed to load audiobook info");
      } finally {
        if (active) setLoading(false);
      }
    }
    loadInfo();
    return () => {
      active = false;
    };
  }, [itemId]);

  const streamUrl =
    info?.tracks && info.tracks.length > 0
      ? `/api/v1/library/audiobooks/${itemId}/tracks/${currentTrackIndex}/stream`
      : `/api/v1/library/audiobooks/${itemId}/stream`;

  const saveProgressToApi = async (locator: Record<string, unknown>, percent: number, completed: boolean) => {
    try {
      await api.put(`/api/v1/library/books/${itemId}/progress`, {
        locator: { ...locator, trackIndex: currentTrackIndex },
        percent,
        completed,
      });
    } catch (err) {
      console.error("Failed to save audiobook progress", err);
    }
  };

  const { handleTimeUpdate, handlePause, handleEnded } = useMediaProgress({
    itemId,
    durationMs: info?.durationMs || durationSec * 1000,
    onSaveProgress: saveProgressToApi,
  });

  const togglePlay = () => {
    if (!audioRef.current) return;
    if (isPlaying) {
      audioRef.current.pause();
    } else {
      audioRef.current.play().catch(console.error);
    }
  };

  const skipSeconds = (seconds: number) => {
    if (!audioRef.current) return;
    audioRef.current.currentTime = Math.max(0, Math.min(audioRef.current.duration, audioRef.current.currentTime + seconds));
  };

  const changeRate = (rate: number) => {
    setPlaybackRate(rate);
    if (audioRef.current) {
      audioRef.current.playbackRate = rate;
    }
  };

  const formatTime = (seconds: number) => {
    if (isNaN(seconds) || seconds < 0) return "0:00";
    const h = Math.floor(seconds / 3600);
    const m = Math.floor((seconds % 3600) / 60);
    const s = Math.floor(seconds % 60);
    if (h > 0) {
      return `${h}:${m.toString().padStart(2, "0")}:${s.toString().padStart(2, "0")}`;
    }
    return `${m}:${s.toString().padStart(2, "0")}`;
  };

  if (loading) {
    return (
      <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 backdrop-blur-sm text-white">
        <div className="flex flex-col items-center space-y-3">
          <div className="h-8 w-8 animate-spin rounded-full border-4 border-amber-500 border-t-transparent" />
          <p className="text-sm font-medium">Loading Audiobook...</p>
        </div>
      </div>
    );
  }

  if (error || !info) {
    return (
      <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 backdrop-blur-sm text-white p-4">
        <div className="rounded-2xl bg-neutral-900 border border-neutral-800 p-6 text-center max-w-sm w-full space-y-4">
          <p className="text-red-400 font-medium">{error || "Audiobook unavailable"}</p>
          <button
            onClick={onClose}
            className="rounded-xl bg-neutral-800 px-4 py-2 text-sm text-neutral-200 hover:bg-neutral-700"
          >
            Close
          </button>
        </div>
      </div>
    );
  }

  return (
    <div
      className="fixed inset-0 z-50 flex flex-col justify-between bg-neutral-950 text-white"
      role="dialog"
      aria-modal="true"
      aria-label={`Audiobook Player: ${info.title}`}
    >
      {/* Top Bar */}
      <div className="flex items-center justify-between border-b border-neutral-800 px-6 py-4">
        <div>
          <h1 className="text-base font-bold text-white leading-tight">{info.title}</h1>
          <p className="text-xs text-neutral-400">{info.author}</p>
        </div>
        <button
          onClick={() => {
            if (audioRef.current) handlePause(audioRef.current.currentTime * 1000);
            onClose();
          }}
          className="rounded-full bg-neutral-900 p-2 text-neutral-400 hover:text-white hover:bg-neutral-800 transition-colors"
          aria-label="Close audiobook player"
        >
          ✕
        </button>
      </div>

      {/* Main Content / Cover Art */}
      <div className="flex flex-1 flex-col items-center justify-center p-6 space-y-6">
        <div className="w-56 h-56 rounded-2xl overflow-hidden bg-neutral-900 border border-neutral-800 shadow-2xl flex items-center justify-center">
          <div className="text-center p-4">
            <span className="text-4xl mb-2 block">🎧</span>
            <p className="text-sm font-medium text-neutral-300 line-clamp-2">{info.title}</p>
            {info.narrator && <p className="text-xs text-neutral-500 mt-1">By {info.narrator}</p>}
          </div>
        </div>

        {/* Track & Chapter Selectors */}
        {info.tracks && info.tracks.length > 1 && (
          <div className="flex items-center gap-2">
            <label className="text-xs text-neutral-400">Track:</label>
            <select
              value={currentTrackIndex}
              onChange={(e) => setCurrentTrackIndex(Number(e.target.value))}
              className="rounded-lg bg-neutral-900 border border-neutral-800 px-3 py-1 text-xs text-neutral-200"
            >
              {info.tracks.map((t) => (
                <option key={t.index} value={t.index}>
                  {t.title || `Track ${t.index + 1}`}
                </option>
              ))}
            </select>
          </div>
        )}
      </div>

      {/* Audio Controller */}
      <audio
        ref={audioRef}
        src={streamUrl}
        onTimeUpdate={() => {
          if (!audioRef.current) return;
          const sec = audioRef.current.currentTime;
          setCurrentTimeSec(sec);
          handleTimeUpdate(sec * 1000);
        }}
        onLoadedMetadata={() => {
          if (!audioRef.current) return;
          setDurationSec(audioRef.current.duration);
          audioRef.current.playbackRate = playbackRate;
        }}
        onPlay={() => setIsPlaying(true)}
        onPause={() => {
          setIsPlaying(false);
          if (audioRef.current) handlePause(audioRef.current.currentTime * 1000);
        }}
        onEnded={() => {
          setIsPlaying(false);
          if (audioRef.current) handleEnded(audioRef.current.currentTime * 1000);
        }}
      />

      {/* Controls Footer */}
      <div className="border-t border-neutral-900 bg-neutral-900/60 p-6 space-y-4 backdrop-blur-md">
        {/* Scrubber */}
        <div className="space-y-1">
          <input
            type="range"
            min={0}
            max={durationSec || 100}
            value={currentTimeSec}
            onChange={(e) => {
              const val = Number(e.target.value);
              setCurrentTimeSec(val);
              if (audioRef.current) audioRef.current.currentTime = val;
            }}
            className="w-full accent-amber-500 bg-neutral-800 h-1.5 rounded-lg cursor-pointer"
            aria-label="Audio scrubber"
          />
          <div className="flex justify-between text-xs text-neutral-400 font-mono">
            <span>{formatTime(currentTimeSec)}</span>
            <span>{formatTime(durationSec)}</span>
          </div>
        </div>

        {/* Buttons */}
        <div className="flex items-center justify-between max-w-md mx-auto">
          {/* Speed */}
          <div className="flex gap-1">
            {[0.75, 1.0, 1.25, 1.5, 2.0].map((rate) => (
              <button
                key={rate}
                onClick={() => changeRate(rate)}
                className={`px-2 py-1 text-xs rounded-md font-mono ${
                  playbackRate === rate
                    ? "bg-amber-500 text-neutral-950 font-bold"
                    : "text-neutral-400 hover:text-white"
                }`}
              >
                {rate}x
              </button>
            ))}
          </div>

          {/* Playback Actions */}
          <div className="flex items-center gap-4">
            <button
              onClick={() => skipSeconds(-15)}
              className="p-2 text-neutral-300 hover:text-white transition-colors"
              aria-label="Skip backward 15 seconds"
            >
              ↺ 15s
            </button>

            <button
              onClick={togglePlay}
              className="flex h-12 w-12 items-center justify-center rounded-full bg-amber-500 text-neutral-950 font-bold hover:bg-amber-400 transition-transform active:scale-95 shadow-lg shadow-amber-500/20"
              aria-label={isPlaying ? "Pause" : "Play"}
            >
              {isPlaying ? "❚❚" : "▶"}
            </button>

            <button
              onClick={() => skipSeconds(15)}
              className="p-2 text-neutral-300 hover:text-white transition-colors"
              aria-label="Skip forward 15 seconds"
            >
              15s ↻
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
