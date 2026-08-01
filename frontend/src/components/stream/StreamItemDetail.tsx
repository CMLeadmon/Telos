import React, { useEffect, useState } from "react";
import { Play, X } from "lucide-react";
import { api } from "@/lib/api";
import { MediaDetail, MediaItem, PlaybackOptions, useMediaStore } from "@/stores/useMediaStore";

interface StreamItemDetailProps {
  itemId: string;
  onClose: () => void;
  onPlay: (item: MediaItem) => void;
}

export function StreamItemDetail({ itemId, onClose, onPlay }: StreamItemDetailProps) {
  const { fetchDetail, fetchPlaybackOptions } = useMediaStore();
  const [detail, setDetail] = useState<MediaDetail | null>(null);
  const [pbOpts, setPbOpts] = useState<PlaybackOptions | null>(null);
  const [related, setRelated] = useState<MediaItem[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let active = true;
    async function load() {
      setLoading(true);
      try {
        const [dRes, pRes, rRes] = await Promise.all([
          fetchDetail(itemId),
          fetchPlaybackOptions(itemId),
          api<MediaItem[]>(`/api/v1/media/items/${encodeURIComponent(itemId)}/related`).catch(() => []),
        ]);
        if (active) {
          setDetail(dRes);
          setPbOpts(pRes);
          setRelated(rRes || []);
        }
      } catch (err) {
        console.error("Failed to load stream item detail", err);
      } finally {
        if (active) setLoading(false);
      }
    }
    load();
    return () => {
      active = false;
    };
  }, [itemId, fetchDetail, fetchPlaybackOptions]);

  if (loading) {
    return (
      <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 backdrop-blur-sm text-white">
        <div className="h-8 w-8 animate-spin rounded-full border-4 border-emerald-500 border-t-transparent" />
      </div>
    );
  }

  if (!detail) {
    return (
      <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 backdrop-blur-sm text-white p-4">
        <div className="rounded-2xl bg-neutral-900 border border-neutral-800 p-6 text-center max-w-sm w-full space-y-4">
          <p className="text-neutral-400 font-medium">Item detail unavailable</p>
          <button onClick={onClose} className="rounded-xl bg-neutral-800 px-4 py-2 text-sm text-neutral-200 hover:bg-neutral-700">
            Close
          </button>
        </div>
      </div>
    );
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 backdrop-blur-md p-4"
      role="dialog"
      aria-modal="true"
      aria-label={`Media Details: ${detail.title}`}
    >
      <div className="relative w-full max-w-2xl max-h-[90vh] overflow-y-auto rounded-3xl bg-neutral-950 border border-neutral-800 p-6 shadow-2xl text-white space-y-6">
        {/* Header */}
        <div className="flex items-start justify-between">
          <div>
            <h1 className="text-2xl font-bold">{detail.title}</h1>
            {detail.seriesName && (
              <p className="text-sm text-emerald-400">
                {detail.seriesName} {detail.seasonName && `• ${detail.seasonName}`} {detail.episodeNumber && `Ep. ${detail.episodeNumber}`}
              </p>
            )}
            <div className="flex items-center gap-3 mt-1 text-xs text-neutral-400">
              {detail.year && <span>{detail.year}</span>}
              {detail.genres && detail.genres.length > 0 && <span>{detail.genres.join(", ")}</span>}
            </div>
          </div>
          <button
            onClick={onClose}
            className="rounded-full bg-neutral-900 p-2 text-neutral-400 hover:text-white hover:bg-neutral-800 transition-colors"
            aria-label="Close detail modal"
          >
            <X size={18} />
          </button>
        </div>

        {/* Play Action */}
        <div className="flex items-center gap-4">
          <button
            onClick={() => {
              onClose();
              onPlay(detail);
            }}
            className="flex items-center gap-2 rounded-2xl bg-emerald-500 px-6 py-3 font-semibold text-neutral-950 hover:bg-emerald-400 transition-transform active:scale-95 shadow-lg shadow-emerald-500/20"
          >
            <Play size={18} fill="currentColor" /> Play
          </button>
          {detail.progress?.percent !== undefined && detail.progress.percent > 0 && (
            <span className="text-xs text-neutral-400 font-mono">
              {Math.round(detail.progress.percent * 100)}% completed
            </span>
          )}
        </div>

        {/* Overview */}
        {detail.overview && (
          <div className="space-y-1">
            <h2 className="text-xs font-semibold text-neutral-400 uppercase tracking-wider">Overview</h2>
            <p className="text-sm text-neutral-300 leading-relaxed">{detail.overview}</p>
          </div>
        )}

        {/* Audio & Subtitle Info */}
        {pbOpts && (
          <div className="grid grid-cols-2 gap-4 rounded-2xl bg-neutral-900/60 p-4 border border-neutral-800/80 text-xs">
            <div>
              <span className="font-semibold text-neutral-400 block mb-1">Audio Tracks</span>
              {pbOpts.audioTracks.length > 0 ? (
                <ul className="space-y-0.5 text-neutral-300">
                  {pbOpts.audioTracks.map((a) => (
                    <li key={a.index}>• {a.title || a.language || `Track ${a.index}`}</li>
                  ))}
                </ul>
              ) : (
                <span className="text-neutral-500">Standard</span>
              )}
            </div>
            <div>
              <span className="font-semibold text-neutral-400 block mb-1">Subtitles</span>
              {pbOpts.subtitles.length > 0 ? (
                <ul className="space-y-0.5 text-neutral-300">
                  {pbOpts.subtitles.map((s) => (
                    <li key={s.index}>• {s.title || s.language || `Sub ${s.index}`}</li>
                  ))}
                </ul>
              ) : (
                <span className="text-neutral-500">None</span>
              )}
            </div>
          </div>
        )}

        {/* Chapters */}
        {detail.chapters && detail.chapters.length > 0 && (
          <div className="space-y-2">
            <h2 className="text-xs font-semibold text-neutral-400 uppercase tracking-wider">Chapters</h2>
            <div className="grid grid-cols-2 gap-2 max-h-36 overflow-y-auto pr-1">
              {detail.chapters.map((ch) => (
                <div key={ch.index} className="rounded-xl bg-neutral-900/40 border border-neutral-800 p-2 text-xs flex justify-between">
                  <span className="text-neutral-300 truncate">{ch.title || `Chapter ${ch.index + 1}`}</span>
                  <span className="text-neutral-500 font-mono ml-2">
                    {Math.floor(ch.startMs / 60000)}:{(Math.floor(ch.startMs / 1000) % 60).toString().padStart(2, "0")}
                  </span>
                </div>
              ))}
            </div>
          </div>
        )}

        {/* Related Media */}
        {related.length > 0 && (
          <div className="space-y-2">
            <h2 className="text-xs font-semibold text-neutral-400 uppercase tracking-wider">More Like This</h2>
            <div className="flex gap-3 overflow-x-auto pb-2">
              {related.map((r) => (
                <button
                  key={r.id}
                  onClick={() => onPlay(r)}
                  className="flex-none w-32 rounded-xl bg-neutral-900 border border-neutral-800 p-3 text-left hover:border-neutral-700 transition-colors"
                >
                  <p className="text-xs font-medium text-neutral-200 line-clamp-2">{r.title}</p>
                </button>
              ))}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
