"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { ImageOff, Info, Music, Settings, Share2 } from "lucide-react";
import { api, apiBase, assetUrl, libraryContentUrl, libraryCoverUrl } from "@/lib/api";
import {
  asList,
  type LibraryBook,
  useLibraryStore,
} from "@/stores/useLibraryStore";
import { useMediaStore, type MediaItem } from "@/stores/useMediaStore";
import { useAuthStore } from "@/stores/useAuthStore";
import { hasCapability } from "@/lib/capabilities";
import { openNodeResource } from "@/lib/platform";
import { BookReader } from "@/components/library/BookReader";
import { BookManageModal } from "@/components/library/BookManageModal";
import { AudiobookPlayer } from "@/components/library/AudiobookPlayer";
import { LibraryItemDetail } from "@/components/library/LibraryItemDetail";

function AudiobookRow({
  item,
  playing,
  onToggle,
}: {
  item: MediaItem;
  playing: boolean;
  onToggle: () => void;
}) {
  return (
    <div className="audiorow">
      <Music size={15} />
      <span className="aname">{item.title}</span>
      <span className="ameta">{item.duration}</span>
      <button
        className="btn-ghost btn-sm"
        aria-label={`play ${item.title}`}
        onClick={onToggle}
      >
        {playing ? "playing" : "play"}
      </button>
      {playing && (
        <audio
          controls
          autoPlay
          data-testid="audiobook-player"
          src={`${apiBase()}/api/v1/stream/audio/${encodeURIComponent(item.id)}`}
        />
      )}
    </div>
  );
}

// A shelf library's direct children are now book folders (Task 1 dropped
// Jellyfin's Recursive=true), so each book needs its own one-level-deeper
// fetch to surface its playable chapters.
function AudiobookBook({
  book,
  playingId,
  onToggle,
}: {
  book: MediaItem;
  playingId: string | null;
  onToggle: (id: string) => void;
}) {
  const { itemsByParent, itemsStatusByParent, fetchItems } = useMediaStore();

  useEffect(() => {
    if (book.isFolder && itemsStatusByParent[book.id] === undefined) {
      void fetchItems(book.id);
    }
  }, [book.id, book.isFolder, itemsStatusByParent, fetchItems]);

  const tracks = book.isFolder ? (itemsByParent[book.id] ?? []) : [book];
  return (
    <>
      {tracks.map((item) => (
        <AudiobookRow
          key={item.id}
          item={item}
          playing={playingId === item.id}
          onToggle={() => onToggle(item.id)}
        />
      ))}
    </>
  );
}

function AudiobookShelf() {
  const { libraries, libraryStatus, itemsByParent, fetchLibraries } =
    useMediaStore();
  const [playingId, setPlayingId] = useState<string | null>(null);

  useEffect(() => {
    if (libraryStatus === "idle") void fetchLibraries();
  }, [libraryStatus, fetchLibraries]);

  // Jellyfin (verified live on 10.11.11) reports no CollectionType at all
  // for "books"-typed libraries via /Users/{id}/Views, and a plain "music"
  // collectionType is indistinguishable from a real music library — so name
  // is the only reliable signal here. Admins name the Jellyfin library with
  // "audiobook" in it (e.g. "Audiobooks").
  const shelves = libraries.filter(
    (lib) => lib.type === "audio" && lib.name.toLowerCase().includes("audiobook"),
  );
  if (shelves.length === 0) return null;

  return (
    <div className="audioshelf">
      {shelves.map((lib) => {
        const books = itemsByParent[lib.id] ?? [];
        if (books.length === 0) return null;
        return (
          <div key={lib.id}>
            <span className="railhead">{`// ${lib.name}`}</span>
            {books.map((book) => (
              <AudiobookBook
                key={book.id}
                book={book}
                playingId={playingId}
                onToggle={(id) => setPlayingId(playingId === id ? null : id)}
              />
            ))}
          </div>
        );
      })}
    </div>
  );
}


function BookCard({
  book,
  onRead,
  onManage,
  onDetail,
  canManage,
}: {
  book: LibraryBook;
  onRead: () => void;
  onManage: () => void;
  onDetail: () => void;
  canManage: boolean;
}) {
  const [coverBroken, setCoverBroken] = useState(false);
  const isEpub = book.format === "EPUB";
  // EPUB, PDF and audiobooks all open in-app; only an unsupported format falls
  // back to a tab. Audiobooks belong here too — without them the card opens a
  // raw content URL instead of the Library player.
  const readable =
    book.format === "EPUB" || book.format === "PDF" || book.kind === "audiobook";
  const open = () => {
    if (readable) onRead();
    // libraryContentUrl absolutizes the origin, but a navigation still carries
    // no bearer token, so in token mode this opened a 401 instead of the book.
    else
      void openNodeResource(
        `/api/v1/library/books/${encodeURIComponent(book.id)}/content`,
        book.title,
      );
  };
  return (
    <article
      className="library-card"
      data-testid="library-card"
      data-format={book.format}
      role="button"
      tabIndex={0}
      aria-label={`${readable ? "read" : "open"} ${book.title}`}
      onClick={open}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          open();
        }
      }}
    >
      <div className="coverwrap">
        {coverBroken ? (
          <ImageOff size={28} />
        ) : (
          <img
            src={assetUrl(libraryCoverUrl(book.id))}
            alt=""
            loading="lazy"
            onError={() => setCoverBroken(true)}
          />
        )}
      </div>
      <div className="btitle" title={book.title}>
        {book.title}
      </div>
      <div className="bauthors">{asList(book.authors).join(", ")}</div>
      <div className="brow">
        <span className={`fmtpill${isEpub ? "" : " pdf"}`}>{book.format}</span>
        <div className="book-actions">
          <button
            className="btn-ghost btn-sm"
            aria-label={`details for ${book.title}`}
            title="Details"
            onClick={(event) => {
              event.stopPropagation();
              onDetail();
            }}
          >
            <Info size={12} />
          </button>
          <a
            className="btn-ghost btn-sm"
            aria-label={`share ${book.title}`}
            title="Share to chat"
            href={`/chat?share_kind=library_book&share_ref=${encodeURIComponent(book.id)}`}
            onClick={(e) => e.stopPropagation()}
          >
            <Share2 size={12} />
          </a>
          {canManage && (
            <button
              className="btn-ghost btn-sm"
              aria-label={`manage ${book.title}`}
              title="Book settings"
              onClick={(event) => {
                event.stopPropagation();
                onManage();
              }}
            >
              <Settings size={12} />
            </button>
          )}
        </div>
      </div>
    </article>
  );
}

export default function LibraryPage() {
  const {
    facets,
    status,
    error,
    filters,
    fetchCatalog,
    setFilter,
    clearFilters,
    filtered,
  } = useLibraryStore();
  const canManage = useAuthStore(
    (state) => hasCapability(state.user, "manage_library"),
  );
  const router = useRouter();
  const [reading, setReading] = useState<LibraryBook | null>(null);
  const [managing, setManaging] = useState<LibraryBook | null>(null);
  const [audioPlayerId, setAudioPlayerId] = useState<string | null>(null);
  const [detailItem, setDetailItem] = useState<LibraryBook | null>(null);

  const books = filtered();

  // Files graduated back to its own module. Forward the old deep link so
  // existing bookmarks and shared chat cards keep resolving.
  useEffect(() => {
    if (new URLSearchParams(window.location.search).get("view") === "files") {
      router.replace("/files");
    }
  }, [router]);

  useEffect(() => {
    if (status === "idle") void fetchCatalog();
  }, [status, fetchCatalog]);

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const readId = params.get("read");
    if (readId && books.length > 0) {
      const found = books.find((b) => b.id === readId);
      if (found) {
        setTimeout(() => setReading(found), 0);
      } else {
        api<LibraryBook>(
          `/api/v1/library/books/${encodeURIComponent(readId)}`,
        )
          .then((resolved) => setReading(resolved))
          .catch(() => {
            // A stale or unauthorized deep link leaves the catalog usable.
          });
      }
      const newUrl = window.location.pathname;
      window.history.replaceState({}, "", newUrl);
    }
  }, [books]);

  const anyFilter =
    filters.author !== null ||
    filters.category !== null ||
    filters.format !== null ||
    filters.search !== "";

  return (
    <>
      {/* The DS Library leads with a tools bar rather than an arenahead and
          banner: format filters as chips, then search, then the count. Authors
          and collections moved to the rail, where the DS puts them. */}
      <div className="libtools">
        <div className="filters">
          <button
            className={`chip${filters.format === null ? " on" : ""}`}
            onClick={() => setFilter("format", null)}
          >
            All
          </button>
          {asList(facets?.formats).map((f) => (
            <button
              key={f.value}
              className={`chip${filters.format === f.value ? " on" : ""}`}
              onClick={() =>
                setFilter("format", filters.format === f.value ? null : f.value)
              }
            >
              {f.value}
            </button>
          ))}
        </div>

        <input
          className="library-search"
          data-testid="library-search"
          placeholder="search title or author…"
          value={filters.search}
          onChange={(e) => setFilter("search", e.target.value)}
        />

        <div className="sortby">
          {status === "ready" &&
            `${books.length} book${books.length === 1 ? "" : "s"}`}
        </div>
        {anyFilter && (
          <button className="btn-ghost btn-sm" onClick={clearFilters}>
            clear filters
          </button>
        )}
      </div>

      <div className="library">
        <div className="library-main">

          {status === "error" && (
            <div className="placeholder">
              <h2>Shelf unreachable.</h2>
              <p>{error}</p>
              <button className="btn-ghost btn-sm" onClick={fetchCatalog}>
                retry
              </button>
            </div>
          )}

          {status === "ready" && books.length === 0 && (
            <div className="placeholder" data-testid="library-empty">
              {anyFilter ? (
                <>
                  <h2>No books match.</h2>
                  <p>loosen the filters or clear them.</p>
                </>
              ) : (
                <>
                  <h2>The shelf is empty.</h2>
                  <p>
                    drop an epub or pdf into the bookdrop from the Files module
                    — it lands here after import.
                  </p>
                </>
              )}
            </div>
          )}

          {books.length > 0 && (
            <div className="library-grid" data-testid="library-grid">
              {books.map((b) => (
                <BookCard
                  key={b.id}
                  book={b}
                  canManage={canManage}
                  onRead={() => {
                    if (b.kind === "audiobook" || b.format === "AUDIOBOOK") {
                      setAudioPlayerId(b.id);
                    } else {
                      setReading(b);
                    }
                  }}
                  onManage={() => setManaging(b)}
                  onDetail={() => setDetailItem(b)}
                />
              ))}
            </div>
          )}

          <AudiobookShelf />
        </div>
      </div>

      {reading && (
        <BookReader book={reading} onClose={() => setReading(null)} />
      )}
      {managing && (
        <BookManageModal
          book={managing}
          onClose={() => setManaging(null)}
        />
      )}
      {audioPlayerId && (
        <AudiobookPlayer
          itemId={audioPlayerId}
          onClose={() => setAudioPlayerId(null)}
        />
      )}
      {detailItem && (
        <LibraryItemDetail
          item={detailItem}
          onClose={() => setDetailItem(null)}
          onOpenItem={(item) => {
            setDetailItem(null);
            if (item.kind === "audiobook") {
              setAudioPlayerId(item.id);
            } else {
              setReading(item);
            }
          }}
        />
      )}
    </>
  );
}
