import { useEffect, useState } from "react";
import { Play, X } from "lucide-react";
import { api } from "@/lib/api";
import {
  type MediaDetail,
  type MediaItem,
  type PlaybackOptions,
  useMediaStore,
} from "@/stores/useMediaStore";

interface StreamItemDetailProps {
  itemId: string;
  onClose: () => void;
  onPlay: (item: MediaItem) => void;
}

function timecode(ms: number): string {
  const total = Math.floor(ms / 1000);
  const m = Math.floor(total / 60);
  const s = total % 60;
  return `${m}:${String(s).padStart(2, "0")}`;
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
          api<MediaItem[]>(
            `/api/v1/media/items/${encodeURIComponent(itemId)}/related`,
          ).catch(() => []),
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

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  if (loading || !detail) {
    return (
      <>
        <div className="idetail-scrim" onClick={onClose} />
        <div
          className="idetail"
          role="dialog"
          aria-modal="true"
          aria-label="Media details"
          data-testid="stream-item-detail"
        >
          <div className="idetail-head">
            <h2>{loading ? "Loading…" : "Unavailable"}</h2>
            <button className="iconbtn" onClick={onClose} aria-label="close details">
              <X size={16} />
            </button>
          </div>
          <div className="idetail-body">
            {/* Jellyfin failing is a recoverable, inline condition — it must not
                erase the browse grid behind this sheet. */}
            {!loading && (
              <p className="idetail-error">
                This item&apos;s details could not be loaded. Streaming may still work.
              </p>
            )}
          </div>
          <div className="idetail-foot">
            <span className="grow" />
            <button className="btn-ghost" onClick={onClose}>
              Close
            </button>
          </div>
        </div>
      </>
    );
  }

  const percent = Math.round((detail.progress?.percent ?? 0) * 100);
  const context = [detail.seriesName, detail.seasonName]
    .filter(Boolean)
    .concat(detail.episodeNumber ? [`Ep. ${detail.episodeNumber}`] : [])
    .join(" · ");

  return (
    <>
      <div className="idetail-scrim" onClick={onClose} />
      <div
        className="idetail"
        role="dialog"
        aria-modal="true"
        aria-labelledby="stream-detail-title"
        data-testid="stream-item-detail"
      >
        <div className="idetail-head">
          <div>
            {context && <div className="idetail-kicker">{context}</div>}
            <h2 id="stream-detail-title">{detail.title}</h2>
          </div>
          <button className="iconbtn" onClick={onClose} aria-label="close details">
            <X size={16} />
          </button>
        </div>

        <div className="idetail-body">
          {detail.coverUrl && (
            <div className="idetail-cover">
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img src={detail.coverUrl} alt="" aria-hidden="true" />
            </div>
          )}

          <div className="idetail-meta">
            <div className="idetail-facts">
              {detail.year && <span>{detail.year}</span>}
              {detail.duration && <span>{detail.duration}</span>}
              {detail.genres && detail.genres.length > 0 && (
                <span>{detail.genres.join(", ")}</span>
              )}
              {detail.studios && detail.studios.length > 0 && (
                <span>{detail.studios.join(", ")}</span>
              )}
            </div>

            {detail.overview && <p className="idetail-desc">{detail.overview}</p>}

            {percent > 0 && (
              <div className="idetail-progress">
                <div className="plabel">
                  <span>progress</span>
                  <span>{percent}%</span>
                </div>
                <div className="idetail-track">
                  <span style={{ width: `${percent}%` }} />
                </div>
              </div>
            )}

            {/* Tracks are listed only when PlaybackInfo confirms them. Selection
                belongs to the player, so this is a manifest, not a control. */}
            {pbOpts && pbOpts.audioTracks.length > 0 && (
              <div className="idetail-section">
                <h3>Audio</h3>
                <div className="idetail-list">
                  {pbOpts.audioTracks.map((a) => (
                    <span key={a.index}>
                      {a.title || a.language || `Track ${a.index}`}
                    </span>
                  ))}
                </div>
              </div>
            )}

            {pbOpts && pbOpts.subtitles.length > 0 && (
              <div className="idetail-section">
                <h3>Subtitles</h3>
                <div className="idetail-list">
                  {pbOpts.subtitles.map((s) => (
                    <span key={s.index}>
                      {s.title || s.language || `Sub ${s.index}`}
                    </span>
                  ))}
                </div>
              </div>
            )}

            {detail.chapters && detail.chapters.length > 0 && (
              <div className="idetail-section">
                <h3>Chapters</h3>
                <div className="idetail-list">
                  {detail.chapters.map((ch) => (
                    <span key={ch.index}>
                      {ch.title || `Chapter ${ch.index + 1}`} · {timecode(ch.startMs)}
                    </span>
                  ))}
                </div>
              </div>
            )}

            {related.length > 0 && (
              <div className="idetail-section">
                <h3>Related</h3>
                <div className="idetail-list">
                  {related.map((r) => (
                    <button key={r.id} onClick={() => onPlay(r)}>
                      {r.title}
                    </button>
                  ))}
                </div>
              </div>
            )}
          </div>
        </div>

        <div className="idetail-foot">
          <button
            className="btn"
            onClick={() => {
              onClose();
              onPlay(detail);
            }}
          >
            <Play size={14} fill="currentColor" />{" "}
            {percent > 0 && !detail.progress?.completed ? "Resume" : "Play"}
          </button>
          <a
            className="btn-ghost"
            href={`/chat?share_kind=stream_film&share_ref=${encodeURIComponent(detail.id)}`}
          >
            Share to chat
          </a>
          <span className="grow" />
          {percent > 0 && <span className="idetail-note">{percent}% watched</span>}
        </div>
      </div>
    </>
  );
}
