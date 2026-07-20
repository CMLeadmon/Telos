"use client";

import { useEffect, useState } from "react";
import { BookOpen, ImageOff, Music, Settings, Share2 } from "lucide-react";
import { apiBase, libraryContentUrl, libraryCoverUrl } from "@/lib/api";
import {
  asList,
  type FacetValue,
  type LibraryBook,
  useLibraryStore,
} from "@/stores/useLibraryStore";
import { useMediaStore, type MediaItem } from "@/stores/useMediaStore";
import { useAuthStore } from "@/stores/useAuthStore";
import { BookReader } from "@/components/library/BookReader";
import { BookManageModal } from "@/components/library/BookManageModal";
import { VaporwaveScene } from "@/components/VaporwaveScene";

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

function FacetGroup({
  title,
  values,
  active,
  onToggle,
}: {
  title: string;
  values: FacetValue[] | null;
  active: string | null;
  onToggle: (value: string | null) => void;
}) {
  const list = asList(values);
  if (list.length === 0) return null;
  return (
    <div className="railgroup">
      <span className="railhead">{`// ${title}`}</span>
      {list.slice(0, 12).map((f) => (
        <button
          key={f.value}
          className={`facetbtn${active === f.value ? " on" : ""}`}
          onClick={() => onToggle(active === f.value ? null : f.value)}
        >
          <span>{f.value}</span>
          <span className="fcount">{f.count}</span>
        </button>
      ))}
    </div>
  );
}

function BookCard({
  book,
  onRead,
  onManage,
  canManage,
}: {
  book: LibraryBook;
  onRead: () => void;
  onManage: () => void;
  canManage: boolean;
}) {
  const [coverBroken, setCoverBroken] = useState(false);
  const isEpub = book.format === "EPUB";
  const open = () => {
    if (isEpub) onRead();
    else window.open(libraryContentUrl(book.id), "_blank", "noopener");
  };
  return (
    <article
      className="library-card"
      data-testid="library-card"
      role="button"
      tabIndex={0}
      aria-label={`${isEpub ? "read" : "open"} ${book.title}`}
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
            src={libraryCoverUrl(book.id)}
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
          <a
            className="btn-ghost btn-sm"
            aria-label={`share ${book.title}`}
            title="Share to chat"
            href={`/chat?share_kind=library_book&share_ref=${book.id}`}
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
    (state) => state.user?.Permissions?.includes("manage_library") ?? false,
  );
  const [reading, setReading] = useState<LibraryBook | null>(null);
  const [managing, setManaging] = useState<LibraryBook | null>(null);

  const books = filtered();

  useEffect(() => {
    if (status === "idle") void fetchCatalog();
  }, [status, fetchCatalog]);

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const readId = params.get("read");
    if (readId && books.length > 0) {
      const found = books.find((b) => String(b.id) === readId);
      if (found) {
        setTimeout(() => setReading(found), 0);
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
      <VaporwaveScene />
      <div className="arenahead">
        <div className="name">
          <BookOpen size={17} />
          Library
        </div>
        <span className="kicker">{`// ${books.length} on the shelf`}</span>
      </div>
      <div className="banner">one shelf for the whole node</div>

      <div className="library">
        <aside className="library-rail">
          <FacetGroup
            title="authors"
            values={facets?.authors ?? null}
            active={filters.author}
            onToggle={(v) => setFilter("author", v)}
          />
          <FacetGroup
            title="categories"
            values={facets?.categories ?? null}
            active={filters.category}
            onToggle={(v) => setFilter("category", v)}
          />
          <FacetGroup
            title="formats"
            values={facets?.formats ?? null}
            active={filters.format}
            onToggle={(v) => setFilter("format", v)}
          />
          {anyFilter && (
            <button className="btn-ghost btn-sm" onClick={clearFilters}>
              clear filters
            </button>
          )}
        </aside>

        <div className="library-main">
          <div className="library-tools">
            <input
              className="library-search"
              data-testid="library-search"
              placeholder="search title or author…"
              value={filters.search}
              onChange={(e) => setFilter("search", e.target.value)}
            />
            {status === "ready" && (
              <span className="library-count">
                {books.length} book{books.length === 1 ? "" : "s"}
              </span>
            )}
          </div>

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
                  onRead={() => setReading(b)}
                  onManage={() => setManaging(b)}
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
    </>
  );
}
