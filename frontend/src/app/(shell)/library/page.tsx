"use client";

import { useEffect, useState } from "react";
import { BookOpen, ExternalLink, ImageOff, Music } from "lucide-react";
import { apiBase, libraryContentUrl, libraryCoverUrl } from "@/lib/api";
import {
  asList,
  type FacetValue,
  type LibraryBook,
  useLibraryStore,
} from "@/stores/useLibraryStore";
import { useMediaStore } from "@/stores/useMediaStore";
import { BookReader } from "@/components/library/BookReader";
import { VaporwaveScene } from "@/components/VaporwaveScene";

function AudiobookShelf() {
  const { libraries, libraryStatus, itemsByLibrary, fetchLibraries } =
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
        const items = itemsByLibrary[lib.id] ?? [];
        if (items.length === 0) return null;
        return (
          <div key={lib.id}>
            <span className="railhead">{`// ${lib.name}`}</span>
            {items.map((item) => (
              <div key={item.id} className="audiorow">
                <Music size={15} />
                <span className="aname">{item.title}</span>
                <span className="ameta">{item.duration}</span>
                <button
                  className="btn-ghost btn-sm"
                  aria-label={`play ${item.title}`}
                  onClick={() =>
                    setPlayingId(playingId === item.id ? null : item.id)
                  }
                >
                  {playingId === item.id ? "playing" : "play"}
                </button>
                {playingId === item.id && (
                  <audio
                    controls
                    autoPlay
                    data-testid="audiobook-player"
                    src={`${apiBase()}/api/v1/stream/audio/${encodeURIComponent(item.id)}`}
                  />
                )}
              </div>
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
}: {
  book: LibraryBook;
  onRead: () => void;
}) {
  const [coverBroken, setCoverBroken] = useState(false);
  const isEpub = book.format === "EPUB";
  return (
    <article className="library-card" data-testid="library-card">
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
        {isEpub ? (
          <button
            className="btn-ghost btn-sm"
            aria-label={`read ${book.title}`}
            onClick={onRead}
          >
            read
          </button>
        ) : (
          <a
            className="btn-ghost btn-sm"
            aria-label={`open ${book.title}`}
            href={libraryContentUrl(book.id)}
            target="_blank"
            rel="noreferrer"
          >
            open <ExternalLink size={12} />
          </a>
        )}
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
  const [reading, setReading] = useState<LibraryBook | null>(null);

  useEffect(() => {
    if (status === "idle") void fetchCatalog();
  }, [status, fetchCatalog]);

  const books = filtered();
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
                <BookCard key={b.id} book={b} onRead={() => setReading(b)} />
              ))}
            </div>
          )}

          <AudiobookShelf />
        </div>
      </div>

      {reading && (
        <BookReader book={reading} onClose={() => setReading(null)} />
      )}
    </>
  );
}
