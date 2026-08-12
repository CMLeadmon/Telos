import { useCallback, useEffect, useRef, useState } from "react";
import { Pause, Play, RotateCcw, RotateCw, X } from "lucide-react";
import { useMediaProgress } from "@/hooks/useMediaProgress";
import { api, apiBase, assetUrl } from "@/lib/api";

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

const RATES = [0.75, 1.0, 1.25, 1.5, 2.0];

function formatTime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return "0:00";
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = Math.floor(seconds % 60);
  if (h > 0) {
    return `${h}:${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`;
  }
  return `${m}:${String(s).padStart(2, "0")}`;
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

  // The saved position is applied once, on the first loadedmetadata for the
  // resumed track. Without this the member's track is restored but playback
  // still starts from zero.
  const pendingSeekRef = useRef<number | null>(null);

  useEffect(() => {
    let active = true;
    async function loadInfo() {
      try {
        setLoading(true);
        const res = await api<AudiobookInfo>(
          `/api/v1/library/audiobooks/${encodeURIComponent(itemId)}/info`,
        );
        if (active && res) {
          setInfo(res);
          const locator = res.progress?.locator;
          if (locator?.trackIndex !== undefined) {
            setCurrentTrackIndex(locator.trackIndex);
          }
          if (locator?.positionMs) {
            pendingSeekRef.current = locator.positionMs / 1000;
          }
        }
      } catch (err: unknown) {
        if (active) {
          setError(
            err instanceof Error ? err.message : "Failed to load audiobook info",
          );
        }
      } finally {
        if (active) setLoading(false);
      }
    }
    loadInfo();
    return () => {
      active = false;
    };
  }, [itemId]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const hasTracks = Boolean(info?.tracks && info.tracks.length > 0);
  // Absolutized. This was a bare relative path handed to an <audio> element,
  // which in the native client resolves against the app bundle rather than the
  // node — the origin-leak gate could not see it, because the bound expression
  // is an identifier and the check only read the expression, never the const.
  //
  // The origin is only half of it: a media element fetches its own source and
  // no header can be attached, so this still needs a credential the URL itself
  // carries before token mode can play an audiobook at all.
  const streamUrl = assetUrl(
    hasTracks
      ? `/api/v1/library/audiobooks/${encodeURIComponent(itemId)}/tracks/${currentTrackIndex}/stream`
      : `/api/v1/library/audiobooks/${encodeURIComponent(itemId)}/stream`,
  );

  const saveProgressToApi = useCallback(
    async (
      locator: Record<string, unknown>,
      percent: number,
      completed: boolean,
    ) => {
      try {
        await api(
          `/api/v1/library/books/${encodeURIComponent(itemId)}/progress`,
          {
            method: "PUT",
            body: JSON.stringify({
              locator: { ...locator, trackIndex: currentTrackIndex },
              percent,
              completed,
            }),
          },
        );
      } catch (err) {
        console.error("Failed to save audiobook progress", err);
      }
    },
    [itemId, currentTrackIndex],
  );

  // The <audio> element reports position within the current track, but the
  // saved percent is against the whole book. Without this offset the bar reset
  // to zero at every track boundary and a finished book read as one track's
  // worth of progress. cumulativeStartMs was already on every track and simply
  // unused.
  const currentTrackStartMs =
    info?.tracks?.[currentTrackIndex]?.cumulativeStartMs ?? 0;
  const toAbsolutePositionMs = useCallback(
    (positionMs: number) => currentTrackStartMs + positionMs,
    [currentTrackStartMs],
  );

  const { handleTimeUpdate, handlePause, handleEnded } = useMediaProgress({
    itemId,
    durationMs: info?.durationMs || durationSec * 1000,
    onSaveProgress: saveProgressToApi,
    toAbsolutePositionMs,
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
    const el = audioRef.current;
    if (!el) return;
    el.currentTime = Math.max(0, Math.min(el.duration || 0, el.currentTime + seconds));
  };

  const changeRate = (rate: number) => {
    setPlaybackRate(rate);
    if (audioRef.current) audioRef.current.playbackRate = rate;
  };

  const selectTrack = (index: number) => {
    setCurrentTrackIndex(index);
    setCurrentTimeSec(0);
    pendingSeekRef.current = null;
  };

  const closeAndSave = () => {
    if (audioRef.current) handlePause(audioRef.current.currentTime * 1000);
    onClose();
  };

  if (loading || error || !info) {
    return (
      <div
        className="abp"
        role="dialog"
        aria-modal="true"
        aria-label="Audiobook player"
      >
        <div className="abp-head">
          <h2>{loading ? "Loading…" : "Unavailable"}</h2>
          <button className="iconbtn" onClick={onClose} aria-label="close player">
            <X size={16} />
          </button>
        </div>
        <div className="abp-body">
          {!loading && (
            <p className="idetail-error">{error || "This audiobook could not be loaded."}</p>
          )}
        </div>
      </div>
    );
  }

  const activeTrack = info.tracks?.[currentTrackIndex];

  return (
    <div
      className="abp"
      role="dialog"
      aria-modal="true"
      aria-label={`Audiobook player: ${info.title}`}
      data-testid="audiobook-player"
    >
      <div className="abp-head">
        <div>
          <h2>{info.title}</h2>
          <p className="by">
            {info.author}
            {info.narrator ? ` · read by ${info.narrator}` : ""}
          </p>
        </div>
        <button className="iconbtn" onClick={closeAndSave} aria-label="close player">
          <X size={16} />
        </button>
      </div>

      <div className="abp-body">
        <div className="abp-cover">
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img
            src={`${apiBase()}/api/v1/library/books/${encodeURIComponent(itemId)}/cover`}
            alt=""
            aria-hidden="true"
            onError={(e) => {
              e.currentTarget.style.display = "none";
            }}
          />
        </div>

        {info.tracks && info.tracks.length > 1 && (
          <div className="abp-tracks">
            <h3>Tracks</h3>
            {info.tracks.map((t) => (
              <button
                key={t.index}
                className={`abp-track${t.index === currentTrackIndex ? " on" : ""}`}
                onClick={() => selectTrack(t.index)}
                aria-current={t.index === currentTrackIndex}
              >
                <span className="tt">{t.title || `Track ${t.index + 1}`}</span>
                <span className="td">{formatTime(t.durationMs / 1000)}</span>
              </button>
            ))}
          </div>
        )}

        {info.chapters && info.chapters.length > 0 && (
          <div className="abp-tracks">
            <h3>Chapters</h3>
            {info.chapters.map((ch) => (
              <div className="abp-track" key={ch.index} role="presentation">
                <span className="tt">{ch.title || `Chapter ${ch.index + 1}`}</span>
                <span className="td">{formatTime(ch.startTimeMs / 1000)}</span>
              </div>
            ))}
          </div>
        )}
      </div>

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
          const el = audioRef.current;
          if (!el) return;
          setDurationSec(el.duration);
          el.playbackRate = playbackRate;
          if (pendingSeekRef.current !== null) {
            const target = pendingSeekRef.current;
            pendingSeekRef.current = null;
            if (Number.isFinite(el.duration) && target < el.duration) {
              el.currentTime = target;
              setCurrentTimeSec(target);
            }
          }
        }}
        onPlay={() => setIsPlaying(true)}
        onPause={() => {
          setIsPlaying(false);
          if (audioRef.current) handlePause(audioRef.current.currentTime * 1000);
        }}
        onEnded={() => {
          const next = currentTrackIndex + 1;
          // A multi-track audiobook is one work: rolling into the next track is
          // the expected behaviour, and only the final track ends the book.
          if (info.tracks && next < info.tracks.length) {
            selectTrack(next);
            // The new source autoplays so listening is continuous.
            requestAnimationFrame(() => audioRef.current?.play().catch(() => {}));
            return;
          }
          setIsPlaying(false);
          if (audioRef.current) handleEnded(audioRef.current.currentTime * 1000);
        }}
      />

      <div className="abp-foot">
        <div>
          <input
            className="abp-scrub"
            type="range"
            min={0}
            max={durationSec || 100}
            value={currentTimeSec}
            onChange={(e) => {
              const val = Number(e.target.value);
              setCurrentTimeSec(val);
              if (audioRef.current) audioRef.current.currentTime = val;
            }}
            aria-label="seek"
          />
          <div className="abp-times">
            <span>{formatTime(currentTimeSec)}</span>
            <span>
              {activeTrack ? `${activeTrack.title || `Track ${currentTrackIndex + 1}`} · ` : ""}
              {formatTime(durationSec)}
            </span>
          </div>
        </div>

        <div className="abp-controls">
          <div className="abp-rates">
            {RATES.map((rate) => (
              <button
                key={rate}
                className={`abp-rate${playbackRate === rate ? " on" : ""}`}
                onClick={() => changeRate(rate)}
                aria-pressed={playbackRate === rate}
              >
                {rate}x
              </button>
            ))}
          </div>

          <div className="abp-transport">
            <button
              className="abp-skip"
              onClick={() => skipSeconds(-15)}
              aria-label="back 15 seconds"
            >
              <RotateCcw size={14} /> 15
            </button>
            <button
              className="abp-play"
              onClick={togglePlay}
              aria-label={isPlaying ? "pause" : "play"}
            >
              {isPlaying ? <Pause size={18} fill="currentColor" /> : <Play size={18} fill="currentColor" />}
            </button>
            <button
              className="abp-skip"
              onClick={() => skipSeconds(15)}
              aria-label="forward 15 seconds"
            >
              15 <RotateCw size={14} />
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
