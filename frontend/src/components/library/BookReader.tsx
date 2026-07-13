"use client";

import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { ChevronLeft, ChevronRight, X } from "lucide-react";
import type { Book, Rendition } from "epubjs";
import { api, libraryContentUrl } from "@/lib/api";
import type { LibraryBook } from "@/stores/useLibraryStore";

interface Progress {
  locator: { cfi?: string; fraction?: number };
  percent: number;
}

interface Relocation {
  start: { cfi: string; percentage: number };
}

export function BookReader({
  book,
  onClose,
}: {
  book: LibraryBook;
  onClose: () => void;
}) {
  const hostRef = useRef<HTMLDivElement>(null);
  const renditionRef = useRef<Rendition | null>(null);
  const [percent, setPercent] = useState(0);
  const [opening, setOpening] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const saveTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    let disposed = false;
    let epub: Book | null = null;
    const host = hostRef.current;
    if (!host) return;

    // epubjs touches the DOM; load it and the book bytes only in the browser.
    Promise.all([
      import("epubjs"),
      fetch(libraryContentUrl(book.id), {
        credentials: "include",
        cache: "no-store",
      }).then(
        (r) => {
          if (!r.ok) throw new Error(`content fetch failed (${r.status})`);
          return r.arrayBuffer();
        },
      ),
      api<Progress>(`/api/v1/library/books/${book.id}/progress`),
    ])
      .then(async ([{ default: ePub }, bytes, progress]) => {
        if (disposed) return;
        epub = ePub(bytes);
        const rendition = epub.renderTo(host, {
          width: "100%",
          height: "100%",
        });
        renditionRef.current = rendition;
        setPercent(progress.percent || 0);

        rendition.on("relocated", (location: Relocation) => {
          const fraction = location.start.percentage || 0;
          const cfi = location.start.cfi;
          // epubjs reports percentage 0 for every relocate fired before
          // locations.generate() resolves (including the initial display
          // of a restored position) — treat that as "not yet known" rather
          // than a real position, so it never clobbers saved progress.
          if (fraction <= 0) return;
          setPercent(fraction);
          if (saveTimer.current) clearTimeout(saveTimer.current);
          saveTimer.current = setTimeout(() => {
            void api(`/api/v1/library/books/${book.id}/progress`, {
              method: "PUT",
              body: JSON.stringify({
                locator: { cfi, fraction },
                percent: fraction,
              }),
            }).catch(() => {}); // progress saving is best-effort
          }, 1000);
        });
        // Keys land in the epub iframe once it has focus — mirror the
        // shortcuts there.
        rendition.on("keydown", (e: KeyboardEvent) => {
          if (e.key === "Escape") onClose();
          if (e.key === "ArrowLeft") rendition.prev();
          if (e.key === "ArrowRight") rendition.next();
        });

        await rendition.display(progress.locator?.cfi || undefined);
        if (disposed) return;
        setOpening(false);
        // Locations power the percentage math; generate after first paint,
        // then restore a fraction-only locator (saved by a non-epubjs client).
        await epub.ready;
        await epub.locations.generate(600);
        if (disposed) return;
        if (!progress.locator?.cfi && progress.locator?.fraction) {
          const cfi = epub.locations.cfiFromPercentage(
            progress.locator.fraction,
          );
          if (cfi) await rendition.display(cfi);
        }
      })
      .catch((err) => {
        if (!disposed) {
          setOpening(false);
          setError(
            err instanceof Error ? err.message : "failed to open book",
          );
        }
      });

    return () => {
      disposed = true;
      if (saveTimer.current) clearTimeout(saveTimer.current);
      renditionRef.current = null;
      epub?.destroy();
      host.replaceChildren();
    };
  }, [book.id, onClose]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
      if (e.key === "ArrowLeft") renditionRef.current?.prev();
      if (e.key === "ArrowRight") renditionRef.current?.next();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  // Portal to the document body: the app shell has an internal stacking
  // context (.shellbody, z-index:4) that caps any z-index set inside it
  // below the topbar (z-index:5) — a plain fixed-position overlay nested
  // in the page tree gets trapped underneath it. No SSR guard needed:
  // BookReader only ever mounts from a client-side click, never prerendered.
  return createPortal(
    <div
      className="reader-overlay"
      data-testid="book-reader"
      role="dialog"
      aria-label={`reading ${book.title}`}
    >
      <header className="reader-bar">
        <span className="reader-title">{book.title}</span>
        <span className="reader-percent" data-testid="reader-percent">
          {Math.round(percent * 100)}%
        </span>
        <button className="iconbtn" aria-label="close reader" onClick={onClose}>
          <X size={18} />
        </button>
      </header>
      {error && <p className="reader-error">{error}</p>}
      {!error && opening && <p className="reader-loading">opening…</p>}
      <div className="reader-host" ref={hostRef} hidden={!!error} />
      {!error && (
        <>
          <button
            className="reader-nav reader-nav-left"
            aria-label="previous page"
            onClick={() => renditionRef.current?.prev()}
          >
            <ChevronLeft />
          </button>
          <button
            className="reader-nav reader-nav-right"
            aria-label="next page"
            onClick={() => renditionRef.current?.next()}
          >
            <ChevronRight />
          </button>
        </>
      )}
    </div>,
    document.body,
  );
}
