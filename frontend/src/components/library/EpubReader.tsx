"use client";

import { useEffect, useRef, useState } from "react";
import { ChevronLeft, ChevronRight } from "lucide-react";
import type { Book, Rendition } from "epubjs";
import { api, libraryContentUrl } from "@/lib/api";
import type { LibraryBook } from "@/stores/useLibraryStore";
import { epubProgressPayload, parseStoredLocator } from "@/lib/reader";

interface Progress {
  locator: { cfi?: string; fraction?: number };
  percent: number;
}

interface Relocation {
  start: { cfi: string; percentage: number };
}

// EpubReader renders an EPUB in-app via epub.js (dynamically imported), saving
// and restoring the CFI/fraction locator through the shared progress route. It
// destroys the book and clears the host on unmount.
export function EpubReader({
  book,
  onClose,
  onPercent,
}: {
  book: LibraryBook;
  onClose: () => void;
  onPercent: (p: number) => void;
}) {
  const hostRef = useRef<HTMLDivElement>(null);
  const renditionRef = useRef<Rendition | null>(null);
  const [opening, setOpening] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const saveTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    let disposed = false;
    let epub: Book | null = null;
    const host = hostRef.current;
    if (!host) return;

    Promise.all([
      import("epubjs"),
      fetch(libraryContentUrl(book.id), { credentials: "include", cache: "no-store" }).then((r) => {
        if (!r.ok) throw new Error(`content fetch failed (${r.status})`);
        return r.arrayBuffer();
      }),
      api<Progress>(`/api/v1/library/books/${book.id}/progress`),
    ])
      .then(async ([{ default: ePub }, bytes, progress]) => {
        if (disposed) return;
        epub = ePub(bytes);
        const rendition = epub.renderTo(host, { width: "100%", height: "100%" });
        renditionRef.current = rendition;
        onPercent(progress.percent || 0);
        const stored = parseStoredLocator("EPUB", progress.locator);

        rendition.on("relocated", (location: Relocation) => {
          const fraction = location.start.percentage || 0;
          const cfi = location.start.cfi;
          if (fraction <= 0) return;
          onPercent(fraction);
          if (saveTimer.current) clearTimeout(saveTimer.current);
          saveTimer.current = setTimeout(() => {
            void api(`/api/v1/library/books/${book.id}/progress`, {
              method: "PUT",
              body: JSON.stringify(epubProgressPayload(cfi, fraction)),
            }).catch(() => {});
          }, 1000);
        });
        rendition.on("keydown", (e: KeyboardEvent) => {
          if (e.key === "Escape") onClose();
          if (e.key === "ArrowLeft") rendition.prev();
          if (e.key === "ArrowRight") rendition.next();
        });

        await rendition.display(stored?.kind === "epub" ? stored.cfi : undefined);
        if (disposed) return;
        setOpening(false);
        await epub.ready;
        await epub.locations.generate(600);
        if (disposed) return;
        if (stored?.kind === "epub" && !stored.cfi && stored.fraction) {
          const cfi = epub.locations.cfiFromPercentage(stored.fraction);
          if (cfi) await rendition.display(cfi);
        }
      })
      .catch((err) => {
        if (!disposed) {
          setOpening(false);
          setError(err instanceof Error ? err.message : "failed to open book");
        }
      });

    return () => {
      disposed = true;
      if (saveTimer.current) clearTimeout(saveTimer.current);
      renditionRef.current = null;
      epub?.destroy();
      host.replaceChildren();
    };
  }, [book.id, onClose, onPercent]);

  return (
    <div className="epub-reader" data-testid="epub-reader">
      {error && <p className="reader-error">{error}</p>}
      {!error && opening && <p className="reader-loading">opening…</p>}
      <div className="reader-host" ref={hostRef} hidden={!!error} />
      {!error && (
        <>
          <button className="reader-nav reader-nav-left" aria-label="previous page" onClick={() => renditionRef.current?.prev()}>
            <ChevronLeft />
          </button>
          <button className="reader-nav reader-nav-right" aria-label="next page" onClick={() => renditionRef.current?.next()}>
            <ChevronRight />
          </button>
        </>
      )}
    </div>
  );
}
