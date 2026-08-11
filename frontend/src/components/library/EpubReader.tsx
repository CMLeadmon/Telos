"use client";

import { useEffect, useRef, useState } from "react";
import { ChevronLeft, ChevronRight } from "lucide-react";
import type { Book, Rendition } from "epubjs";
import { api } from "@/lib/api";
import type { LibraryBook } from "@/stores/useLibraryStore";
import { epubProgressPayload, parseStoredLocator } from "@/lib/reader";
import type { AnnotationLocator } from "@/stores/useAnnotationStore";

interface Progress {
  locator: { cfi?: string; fraction?: number };
  percent: number;
}

interface Relocation {
  start: { cfi: string; percentage: number };
}

// The bundled epub.js typings still describe the 0.3 API (a synchronous
// factory, book.ready, book.locations.generate). The installed runtime is
// 0.4.2, where the factory is async and generateLocations both builds and
// attaches the locations. Describe only what this reader uses.
type EpubBook = Book & {
  generateLocations: (chars: number) => Promise<unknown>;
};

// EpubReader renders an EPUB in-app via epub.js (dynamically imported), saving
// and restoring the CFI/fraction locator through the shared progress route. It
// destroys the book and clears the host on unmount.
export function EpubReader({
  book,
  onClose,
  onPercent,
  onSelection,
}: {
  book: LibraryBook;
  onClose: () => void;
  onPercent: (p: number) => void;
  onSelection?: (locator: AnnotationLocator, text: string) => void;
}) {
  const hostRef = useRef<HTMLDivElement>(null);
  const renditionRef = useRef<Rendition | null>(null);
  // 0.4's Rendition reads this.locations to fill in a relocation's percentage
  // but nothing ever assigns it, so start.percentage is always 0. Keep the
  // generated locations here and compute the fraction ourselves.
  const locationsRef = useRef<{ percentageFromCfi: (cfi: string) => number } | null>(null);

  // The open effect must key on the book alone. onClose/onPercent/onSelection
  // arrive as fresh closures on every parent render, and with them in the
  // dependency list the reader destroys and re-downloads the book each time it
  // reports progress — which also strips the nav buttons mid-read.
  const cbRef = useRef({ onClose, onPercent, onSelection });
  useEffect(() => {
    cbRef.current = { onClose, onPercent, onSelection };
  }, [onClose, onPercent, onSelection]);
  const [opening, setOpening] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const saveTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    let disposed = false;
    let epub: EpubBook | null = null;
    const host = hostRef.current;
    if (!host) return;

    Promise.all([
      import("epubjs"),
      // no-store was on the raw fetch this replaced. A replaced book file keeps
      // its URL, so a cached copy would open the previous edition.
      api<ArrayBuffer>(
        `/api/v1/library/books/${encodeURIComponent(book.id)}/content`,
        { cache: "no-store" },
      ),
      api<Progress>(
        `/api/v1/library/books/${encodeURIComponent(book.id)}/progress`,
      ),
    ])
      .then(async ([{ default: ePub }, bytes, progress]) => {
        if (disposed) return;
        // Two epub.js 0.4 changes, both fatal to the 0.3-era call this
        // replaces. The factory is async, so it must be awaited. And it
        // branches on arguments.length: called with a single object it treats
        // it as an options bag and returns a bare Epub with no renderTo, so an
        // ArrayBuffer has to be passed with an explicit options argument.
        epub = (await ePub(bytes, {})) as unknown as EpubBook;
        if (disposed) return;
        // 0.4 still reaches for a bare global `ePub` in several places — the
        // rendition's manager/view lookup, and section navigation, which is
        // why paging threw "ePub is not defined" and never advanced. Only the
        // UMD build defines it, so publish it before anything renders.
        (window as unknown as { ePub?: unknown }).ePub = ePub;
        const registry = ePub as unknown as {
          ViewManagers?: Record<string, unknown>;
          Views?: Record<string, unknown>;
        };
        const rendition = epub.renderTo(host, {
          width: "100%",
          height: "100%",
          manager: registry.ViewManagers?.default,
          view: registry.Views?.iframe,
        } as Parameters<Book["renderTo"]>[1]);
        renditionRef.current = rendition;
        cbRef.current.onPercent(progress.percent || 0);
        const stored = parseStoredLocator("EPUB", progress.locator);

        rendition.on("relocated", (location: Relocation) => {
          const cfi = location.start.cfi;
          const fraction =
            location.start.percentage ||
            locationsRef.current?.percentageFromCfi(cfi) ||
            0;
          if (fraction <= 0) return;
          cbRef.current.onPercent(fraction);
          if (saveTimer.current) clearTimeout(saveTimer.current);
          saveTimer.current = setTimeout(() => {
            void api(`/api/v1/library/books/${encodeURIComponent(book.id)}/progress`, {
              method: "PUT",
              body: JSON.stringify(epubProgressPayload(cfi, fraction)),
            }).catch(() => {});
          }, 1000);
        });
        rendition.on("keydown", (e: KeyboardEvent) => {
          if (e.key === "Escape") cbRef.current.onClose();
          if (e.key === "ArrowLeft") rendition.prev();
          if (e.key === "ArrowRight") rendition.next();
        });
        // A highlight selection yields a CFI range locator for an annotation.
        rendition.on("selected", (cfiRange: string, contents: { window: Window }) => {
          const text = contents.window.getSelection?.()?.toString() ?? "";
          cbRef.current.onSelection?.({ kind: "epub", cfi: cfiRange }, text);
        });

        await rendition.display(stored?.kind === "epub" ? stored.cfi : undefined);
        if (disposed) return;
        setOpening(false);
        // 0.4 replaced `ready` + `locations.generate` with one call that also
        // populates book.locations. Its Rendition reads this.locations to
        // compute a relocation's percentage but never assigns it, so without
        // handing them over the progress readout stays frozen forever.
        // generateLocations in 0.4.2 assigns its result to an undeclared
        // `book`, so it always rejects — after having generated the locations
        // successfully. Letting that reject would blank the reader, so recover
        // them from the Epub instance the factory publishes on window.
        let locations: unknown = null;
        try {
          locations = await epub.generateLocations(600);
        } catch {
          locations =
            (window as unknown as { Epub?: { locations?: unknown } }).Epub
              ?.locations ?? null;
        }
        if (disposed) return;
        if (locations) {
          locationsRef.current = locations as {
            percentageFromCfi: (cfi: string) => number;
          };
          (rendition as unknown as { locations?: unknown }).locations = locations;
          // The first relocation already fired while locations were still
          // building, so report the current position now that they exist.
          const current = (
            rendition as unknown as { currentLocation: () => Relocation | undefined }
          ).currentLocation();
          if (current?.start?.cfi) {
            const fraction = locationsRef.current.percentageFromCfi(current.start.cfi);
            if (fraction > 0) cbRef.current.onPercent(fraction);
          }
        }
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
      // 0.4's book.destroy also tears down the rendition, which throws if the
      // book never got as far as renderTo (an aborted or failed open).
      try {
        epub?.destroy();
      } catch {
        // The host is cleared below either way.
      }
      host.replaceChildren();
    };
  }, [book.id]);

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
