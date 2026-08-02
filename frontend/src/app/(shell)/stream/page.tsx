"use client";

import { useEffect, useState } from "react";
import {
  AlertCircle,
  CheckCircle2,
  ChevronRight,
  Play,
  RefreshCw,
  Share2,
  X,
} from "lucide-react";
import { api, ApiError, apiBase } from "@/lib/api";
import {
  useMediaStore,
  type MediaItem,
  type MediaLibrary,
} from "@/stores/useMediaStore";
import { useAuthStore } from "@/stores/useAuthStore";
import { hasCapability } from "@/lib/capabilities";
import { VaporwaveScene } from "@/components/VaporwaveScene";
import { HlsPlayer } from "@/components/stream/HlsPlayer";
import { CommentaryPanel } from "@/components/CommentaryPanel";
import { MediaShelf } from "@/components/stream/MediaShelf";
import { StreamItemDetail } from "@/components/stream/StreamItemDetail";
import { DARK_TEXT, posterClass, posterMeta } from "@/components/stream/poster";

interface StreamItemResponse {
  id: string;
  title: string;
  durationSec?: number;
  kind: string;
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
            >
              {item.coverUrl ? (
                // eslint-disable-next-line @next/next/no-img-element
                <img className="pcover" src={item.coverUrl} alt="" aria-hidden="true" />
              ) : (
                <div className="motif" />
              )}
              <span className="pt">{item.title}</span>
              <span className="pm">{posterMeta(item)}</span>
            </button>
            {!item.isFolder && (
              <a
                className="poster-share"
                href={`/chat?share_kind=stream_film&share_ref=${encodeURIComponent(item.id)}`}
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
    (s) => hasCapability(s.user, "manage_files"),
  );
  const canModerateComments = useAuthStore(
    (s) => hasCapability(s.user, "moderate_annotations"),
  );
  const {
    libraries,
    libraryStatus,
    error,
    activeLibraryId,
    itemsByParent,
    itemsStatusByParent,
    continueItems,
    recentItems,
    path,
    rootLibrary,
    nowPlaying,
    refreshing,
    refreshPhase,
    refreshProgress,
    refreshNotice,
    fetchLibraries,
    fetchContinue,
    fetchRecent,
    refresh,
    clearRefreshNotice,
    open,
    navigateTo,
    play,
    stop,
  } = useMediaStore();
  const [detailItemId, setDetailItemId] = useState<string | null>(null);

  useEffect(() => {
    if (libraryStatus === "idle") void fetchLibraries();
    void fetchContinue();
    void fetchRecent();
  }, [libraryStatus, fetchLibraries, fetchContinue, fetchRecent]);

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const playId = params.get("play");
    if (playId) {
      api<StreamItemResponse>(
        `/api/v1/media/items/${encodeURIComponent(playId)}`,
      )
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
          <HlsPlayer
            src={streamUrl(nowPlaying.item, nowPlaying.library)}
            audio={isAudioItem(nowPlaying.item, nowPlaying.library)}
          />
          <CommentaryPanel
            targetType="media"
            targetId={nowPlaying.item.id}
            canModerate={canModerateComments}
          />
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

        {continueItems.length > 0 && (
          <MediaShelf
            title="Continue Watching"
            items={continueItems}
            onSelectItem={(item) => setDetailItemId(item.id)}
          />
        )}

        {recentItems.length > 0 && (
          <MediaShelf
            title="Recently Added"
            items={recentItems}
            onSelectItem={(item) => setDetailItemId(item.id)}
          />
        )}

        {libraries.map((lib) => {
          const items = itemsByParent[lib.id] ?? [];
          const status = itemsStatusByParent[lib.id] ?? "idle";
          return (
            <section className="row" key={lib.id} id={`lib-${lib.id}`}>
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
                onOpen={(item) => {
                  if (item.isFolder) {
                    open(item, lib);
                  } else {
                    setDetailItemId(item.id);
                  }
                }}
              />
            </section>
          );
        })}
      </div>

      {detailItemId && (
        <StreamItemDetail
          itemId={detailItemId}
          onClose={() => setDetailItemId(null)}
          onPlay={(item) => {
            setDetailItemId(null);
            const lib = activeLibrary || libraries[0] || { id: "root", name: "Shared Stream", type: "video" };
            play(item, lib);
          }}
        />
      )}
    </div>
  );
}
