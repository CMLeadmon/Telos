"use client";

import { useEffect, useRef, useState } from "react";
import {
  AlertCircle,
  CheckCircle2,
  ChevronRight,
  Play,
  Plus,
  RefreshCw,
  Share2,
  Users,
  X,
} from "lucide-react";
import type Hls from "hls.js";
import { api, ApiError, apiBase } from "@/lib/api";
import {
  useMediaStore,
  type MediaItem,
  type MediaLibrary,
} from "@/stores/useMediaStore";
import { useAuthStore } from "@/stores/useAuthStore";
import { VaporwaveScene } from "@/components/VaporwaveScene";
import { MyListShelf } from "@/components/stream/MyListShelf";
import { WatchPartyPanel } from "@/components/stream/WatchPartyPanel";

const POSTER_CLASSES = ["c0", "c1", "c2", "c3", "c4", "c5", "c6", "c7"];
const DARK_TEXT = new Set(["c2", "c6"]);

interface StreamItemResponse {
  id: string;
  title: string;
  durationSec?: number;
  kind: string;
}

function posterClass(id: string): string {
  let h = 0;
  for (const ch of id) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  return POSTER_CLASSES[h % POSTER_CLASSES.length];
}

function isAudioItem(item: MediaItem, library: MediaLibrary): boolean {
  return (
    library.type === "audio" ||
    item.type === "Audio" ||
    item.type === "Audiobook"
  );
}

function streamUrl(item: MediaItem, library: MediaLibrary): string {
  const kind = isAudioItem(item, library) ? "audio" : "video";
  return `${apiBase()}/api/v1/stream/${kind}/${encodeURIComponent(item.id)}`;
}

// A folder's meta line describes what's inside instead of a duration —
// folders (series, seasons, audiobooks) carry no runtime of their own.
function folderNoun(type: string): string {
  switch (type) {
    case "Series":
      return "season";
    case "Season":
      return "episode";
    default:
      return "item";
  }
}

function posterMeta(item: MediaItem): string {
  if (item.isFolder) {
    const count = item.childCount ?? 0;
    const noun = folderNoun(item.type);
    return `${count} ${noun}${count === 1 ? "" : "s"}`;
  }
  return `${item.duration} · ${item.type.toLowerCase()}`;
}

// Video items stream as HLS: the gateway resolves PlaybackInfo and 302s to a
// main.m3u8 sub-path. hls.js follows the redirect and resolves segments
// against the final URL; Safari plays it natively. Audio is a static proxy.
function MediaPlayer({ item, library }: { item: MediaItem; library: MediaLibrary }) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const [error, setError] = useState<string | null>(null);
  const audio = isAudioItem(item, library);
  const src = streamUrl(item, library);

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    setError(null);

    if (audio || video.canPlayType("application/vnd.apple.mpegurl")) {
      video.src = src;
      return () => {
        video.removeAttribute("src");
        video.load();
      };
    }

    let hls: Hls | null = null;
    let cancelled = false;
    void import("hls.js").then(({ default: HlsCtor }) => {
      if (cancelled) return;
      if (!HlsCtor.isSupported()) {
        setError("hls playback is not supported in this browser");
        return;
      }
      hls = new HlsCtor({
        // Include the session cookie on manifest and segment requests.
        xhrSetup: (xhr) => {
          xhr.withCredentials = true;
        },
      });
      hls.on(HlsCtor.Events.ERROR, (_event, data) => {
        if (data.fatal) {
          setError(`stream failed: ${data.details}`);
          hls?.destroy();
        }
      });
      hls.loadSource(src);
      hls.attachMedia(video);
    });

    return () => {
      cancelled = true;
      hls?.destroy();
    };
  }, [src, audio]);

  return (
    <div className="playerstage">
      {error ? (
        <p className="playererr">{`// ${error}`}</p>
      ) : (
        <video
          ref={videoRef}
          controls
          autoPlay
          playsInline
          crossOrigin={apiBase() ? "use-credentials" : undefined}
          data-testid="stream-video"
        />
      )}
    </div>
  );
}

function PosterGrid({
  items,
  status,
  onOpen,
}: {
  items: MediaItem[];
  status: "idle" | "loading" | "ready" | "error";
  onOpen: (item: MediaItem) => void;
}) {
  if (status === "error") {
    return <p className="desc">{`// this folder is unavailable`}</p>;
  }
  if (status === "ready" && items.length === 0) {
    return <p className="desc">{`// nothing here yet`}</p>;
  }
  return (
    <div className="posters">
      {items.map((item) => {
        const cls = posterClass(item.id);
        return (
          <div key={item.id} className="posterwrap">
            <button
              className={`poster ${cls}${DARK_TEXT.has(cls) ? " pdark" : ""}`}
              data-testid={item.isFolder ? "poster-folder" : "poster-leaf"}
              onClick={() => onOpen(item)}
              style={{ width: "100%" }}
            >
              <div className="motif" />
              <span className="pt">{item.title}</span>
              <span className="pm">{posterMeta(item)}</span>
            </button>
            {!item.isFolder && (
              <a
                className="poster-share"
                href={`/chat?share_kind=stream_film&share_ref=${item.id}`}
                title="Share to chat"
                aria-label={`share ${item.title} to chat`}
              >
                <Share2 size={13} />
              </a>
            )}
          </div>
        );
      })}
    </div>
  );
}

export default function StreamPage() {
  const [sharedItemError, setSharedItemError] = useState<string | null>(null);
  const canRefreshMedia = useAuthStore(
    (s) =>
      s.user?.Roles.some((role) =>
        ["Owner", "Administrator", "Librarian"].includes(role),
      ) ?? false,
  );
  const {
    libraries,
    libraryStatus,
    error,
    activeLibraryId,
    itemsByParent,
    itemsStatusByParent,
    path,
    rootLibrary,
    nowPlaying,
    refreshing,
    refreshPhase,
    refreshProgress,
    refreshNotice,
    fetchLibraries,
    refresh,
    clearRefreshNotice,
    open,
    navigateTo,
    play,
    stop,
  } = useMediaStore();

  useEffect(() => {
    if (libraryStatus === "idle") void fetchLibraries();
  }, [libraryStatus, fetchLibraries]);

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const playId = params.get("play");
    if (playId) {
      api<StreamItemResponse>(`/api/v1/media/items/${playId}`)
        .then((item) => {
          if (item) {
            const mediaItem: MediaItem = {
              id: item.id,
              title: item.title,
              duration: item.durationSec ? `${Math.round(item.durationSec / 60)}m` : "",
              type: item.kind === "audio" || item.kind === "audiobook" ? "Audio" : "Movie",
              isFolder: false,
            };
            const mediaLibrary: MediaLibrary = {
              id: "root",
              name: "Shared Stream",
              type: item.kind === "audio" || item.kind === "audiobook" ? "audio" : "video",
            };
            play(mediaItem, mediaLibrary);
          }
        })
        .catch((err) => {
          setSharedItemError(
            err instanceof ApiError && err.status === 404
              ? "This media is no longer available. It may have been removed from shared storage."
              : "The shared media item could not be loaded.",
          );
        });

      const newUrl = window.location.pathname;
      window.history.replaceState({}, "", newUrl);
    }
  }, [play]);

  const activeLibrary =
    libraries.find((l) => l.id === activeLibraryId) ?? libraries[0];
  const featured = activeLibrary
    ? (itemsByParent[activeLibrary.id] ?? [])[0]
    : undefined;
  const refreshLabel = refreshing
    ? refreshPhase === "starting"
      ? "starting scan…"
      : refreshPhase === "refreshing"
        ? "refreshing catalog…"
        : refreshProgress !== null
          ? `scanning ${Math.round(refreshProgress)}%…`
          : "rescanning…"
    : refreshPhase === "complete"
      ? "scan complete"
      : "rescan jellyfin";

  if (nowPlaying) {
    return (
      <div className="streammain" data-testid="stream-player">
        <div className="playerwrap">
          <div className="playerhead">
            <button className="iconbtn" aria-label="stop playback" onClick={stop}>
              <X size={18} />
            </button>
            <span className="ptitle">{nowPlaying.item.title}</span>
            <span className="kicker">
              {isAudioItem(nowPlaying.item, nowPlaying.library)
                ? "// direct stream"
                : "// hls · main.m3u8"}
            </span>
          </div>
          <MediaPlayer item={nowPlaying.item} library={nowPlaying.library} />
        </div>
      </div>
    );
  }

  if (path.length > 0 && rootLibrary) {
    const current = path[path.length - 1];
    const items = itemsByParent[current.id] ?? [];
    const status = itemsStatusByParent[current.id] ?? "idle";
    return (
      <div className="streammain" data-testid="stream-browse">
        <div className="streamscroll">
          <nav className="crumbs" data-testid="stream-crumbs">
            <button className="crumb" onClick={() => navigateTo(-1)}>
              Home
            </button>
            {path.map((crumb, i) => (
              <span key={crumb.id} className="crumbseg">
                <ChevronRight size={13} />
                <button
                  className="crumb"
                  disabled={i === path.length - 1}
                  onClick={() => navigateTo(i)}
                >
                  {crumb.title}
                </button>
              </span>
            ))}
          </nav>
          <section className="row">
            <PosterGrid
              items={items}
              status={status}
              onOpen={(item) => open(item, rootLibrary)}
            />
          </section>
        </div>
      </div>
    );
  }

  return (
    <div className="streammain" data-testid="stream-browse">
      <div className="streamscroll">
        <WatchPartyPanel />
        <MyListShelf />
        {sharedItemError && (
          <div className="streamnotice error" role="alert">
            <AlertCircle size={16} />
            <span>{sharedItemError}</span>
            <button
              className="iconbtn"
              aria-label="dismiss media notice"
              onClick={() => setSharedItemError(null)}
            >
              <X size={15} />
            </button>
          </div>
        )}
        <section className="hero2">
          <VaporwaveScene />
          <div className="scrim" />
          <div className="hc">
            <span className="fchip">Featured on the node</span>
            {libraryStatus === "error" ? (
              <>
                <h1>Signal lost.</h1>
                <p className="desc">{`// ${error}`}</p>
              </>
            ) : featured ? (
              <>
                <h1>{featured.title}</h1>
                <div className="metarow">
                  <span>{posterMeta(featured)}</span>
                  <span className="b">{featured.type}</span>
                  {activeLibrary && <span className="b">{activeLibrary.name}</span>}
                </div>
                <div className="cta">
                  <button
                    className="btn rose btn-lg"
                    onClick={() =>
                      activeLibrary &&
                      (featured.isFolder
                        ? open(featured, activeLibrary)
                        : play(featured, activeLibrary))
                    }
                  >
                    <Play size={17} /> {featured.isFolder ? "Open" : "Play"}
                  </button>
                  <button className="btn-ghost btn-lg">
                    <Plus size={17} /> My list
                  </button>
                  <button className="btn-ghost btn-lg">
                    <Users size={17} /> Watch party
                  </button>
                </div>
              </>
            ) : (
              <>
                <h1>The projector is warming up.</h1>
                <p className="desc">
                  {libraryStatus === "ready"
                    ? "no media on the node yet — drop files into the shared library and jellyfin will pick them up."
                    : "tuning into jellyfin…"}
                </p>
              </>
            )}
          </div>
        </section>

        <div className="streamtools">
          <span className="kicker">{`// on the node`}</span>
          {canRefreshMedia && (
            <button
              className="btn-ghost btn-sm"
              data-testid="stream-refresh"
              disabled={refreshing}
              onClick={() => void refresh()}
            >
              {refreshPhase === "complete" ? (
                <CheckCircle2 size={13} />
              ) : (
                <RefreshCw size={13} className={refreshing ? "spin" : undefined} />
              )}{" "}
              {refreshLabel}
            </button>
          )}
        </div>

        {refreshNotice && (
          <div
            className={`streamnotice ${refreshNotice.kind}`}
            data-testid="stream-refresh-notice"
            role={refreshNotice.kind === "error" ? "alert" : "status"}
          >
            {refreshNotice.kind === "success" ? (
              <CheckCircle2 size={16} />
            ) : (
              <AlertCircle size={16} />
            )}
            <span>{refreshNotice.text}</span>
            <button
              className="iconbtn"
              aria-label="dismiss scan notice"
              onClick={clearRefreshNotice}
            >
              <X size={15} />
            </button>
          </div>
        )}

        {libraries.map((lib) => {
          const items = itemsByParent[lib.id] ?? [];
          const status = itemsStatusByParent[lib.id] ?? "idle";
          return (
            <section className="row" key={lib.id}>
              <div className="rowhead">
                <h2>{lib.name}</h2>
                <span className="more">
                  {status === "error"
                    ? "unavailable"
                    : status === "ready"
                      ? `${items.length} item${items.length === 1 ? "" : "s"}`
                      : "…"}
                </span>
              </div>
              <PosterGrid
                items={items}
                status={status}
                onOpen={(item) => open(item, lib)}
              />
            </section>
          );
        })}
      </div>
    </div>
  );
}
