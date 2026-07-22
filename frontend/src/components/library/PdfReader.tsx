"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { ChevronLeft, ChevronRight, ZoomIn, ZoomOut } from "lucide-react";
import { api, libraryContentUrl } from "@/lib/api";
import type { LibraryBook } from "@/stores/useLibraryStore";
import { clampPage, clampZoom, pdfProgressPayload, parseStoredLocator } from "@/lib/reader";

interface Progress {
  locator: { page?: number; zoom?: number };
  percent: number;
}

// Minimal shapes for the parts of the PDF.js API we use.
type PdfPage = {
  getViewport: (o: { scale: number }) => { width: number; height: number };
  render: (o: { canvasContext: CanvasRenderingContext2D; viewport: unknown }) => { promise: Promise<void>; cancel: () => void };
  getTextContent: () => Promise<unknown>;
};
type PdfDoc = { numPages: number; getPage: (n: number) => Promise<PdfPage>; destroy: () => Promise<void> };

// PdfReader renders a PDF in-app with PDF.js (dynamically imported only here),
// drawing the current page to a canvas plus a selectable text layer. It restores
// and persists page/zoom through the shared progress route and terminates the
// worker + render task on unmount.
export function PdfReader({
  book,
  onPercent,
}: {
  book: LibraryBook;
  onPercent: (p: number) => void;
}) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const textLayerRef = useRef<HTMLDivElement>(null);
  const docRef = useRef<PdfDoc | null>(null);
  const renderTaskRef = useRef<{ cancel: () => void } | null>(null);
  const pdfjsRef = useRef<typeof import("pdfjs-dist") | null>(null);
  const saveTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const [page, setPage] = useState(1);
  const [zoom, setZoom] = useState(1);
  const [total, setTotal] = useState(0);
  const [opening, setOpening] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const renderPage = useCallback(async (pageNum: number, scale: number) => {
    const doc = docRef.current;
    const canvas = canvasRef.current;
    const pdfjs = pdfjsRef.current;
    if (!doc || !canvas || !pdfjs) return;
    if (renderTaskRef.current) renderTaskRef.current.cancel();
    const pdfPage = await doc.getPage(pageNum);
    const viewport = pdfPage.getViewport({ scale });
    const ctx = canvas.getContext("2d");
    if (!ctx) return;
    canvas.width = viewport.width;
    canvas.height = viewport.height;
    const task = pdfPage.render({ canvasContext: ctx, viewport });
    renderTaskRef.current = task;
    await task.promise;

    // Selectable text layer over the canvas.
    const textLayer = textLayerRef.current;
    if (textLayer) {
      textLayer.replaceChildren();
      textLayer.style.width = `${viewport.width}px`;
      textLayer.style.height = `${viewport.height}px`;
      try {
        const textContent = await pdfPage.getTextContent();
        const TextLayer = (pdfjs as unknown as { TextLayer?: new (o: unknown) => { render: () => Promise<void> } }).TextLayer;
        if (TextLayer) {
          await new TextLayer({ textContentSource: textContent, container: textLayer, viewport }).render();
        }
      } catch {
        // A text-layer failure never blocks the visual (canvas) render.
      }
    }
  }, []);

  // Load PDF.js and the document once.
  useEffect(() => {
    let disposed = false;
    (async () => {
      try {
        const pdfjs = await import("pdfjs-dist");
        // The worker is bundled and emitted by the app builder.
        pdfjs.GlobalWorkerOptions.workerSrc = new URL(
          "pdfjs-dist/build/pdf.worker.min.mjs",
          import.meta.url,
        ).toString();
        pdfjsRef.current = pdfjs;

        const progress = await api<Progress>(`/api/v1/library/books/${book.id}/progress`);
        // withCredentials lets PDF.js issue authenticated range requests.
        const doc = (await pdfjs.getDocument({
          url: libraryContentUrl(book.id),
          withCredentials: true,
        }).promise) as unknown as PdfDoc;
        if (disposed) {
          void doc.destroy();
          return;
        }
        docRef.current = doc;
        setTotal(doc.numPages);

        const stored = parseStoredLocator("PDF", progress.locator);
        const startPage = stored?.kind === "pdf" ? clampPage(stored.page, doc.numPages) : 1;
        const startZoom = stored?.kind === "pdf" ? clampZoom(stored.zoom) : 1;
        setPage(startPage);
        setZoom(startZoom);
        onPercent(progress.percent || 0);
        await renderPage(startPage, startZoom);
        if (!disposed) setOpening(false);
      } catch (err) {
        if (!disposed) {
          setOpening(false);
          setError(err instanceof Error ? err.message : "failed to open PDF");
        }
      }
    })();

    return () => {
      disposed = true;
      if (saveTimer.current) clearTimeout(saveTimer.current);
      if (renderTaskRef.current) renderTaskRef.current.cancel();
      void docRef.current?.destroy();
      docRef.current = null;
      pdfjsRef.current = null;
    };
  }, [book.id, onPercent, renderPage]);

  // Re-render + persist on page/zoom change (after the initial open).
  useEffect(() => {
    if (opening || !docRef.current) return;
    void renderPage(page, zoom);
    onPercent(total > 0 ? page / total : 0);
    if (saveTimer.current) clearTimeout(saveTimer.current);
    saveTimer.current = setTimeout(() => {
      void api(`/api/v1/library/books/${book.id}/progress`, {
        method: "PUT",
        body: JSON.stringify(pdfProgressPayload(page, zoom, total)),
      }).catch(() => {});
    }, 800);
  }, [page, zoom, total, opening, book.id, onPercent, renderPage]);

  const go = (delta: number) => setPage((p) => clampPage(p + delta, total));

  return (
    <div className="pdf-reader" data-testid="pdf-reader">
      {error && <p className="reader-error">{error}</p>}
      {!error && opening && <p className="reader-loading">opening…</p>}
      <div className="pdf-scroll" hidden={!!error}>
        <div className="pdf-page">
          <canvas ref={canvasRef} data-testid="pdf-canvas" />
          <div ref={textLayerRef} className="pdf-text-layer" data-testid="pdf-text-layer" />
        </div>
      </div>
      {!error && (
        <div className="pdf-controls">
          <button aria-label="previous page" onClick={() => go(-1)} disabled={page <= 1}>
            <ChevronLeft size={16} />
          </button>
          <span className="pdf-pageinfo" data-testid="pdf-pageinfo">
            {page} / {total || "…"}
          </span>
          <button aria-label="next page" onClick={() => go(1)} disabled={total > 0 && page >= total}>
            <ChevronRight size={16} />
          </button>
          <button aria-label="zoom out" onClick={() => setZoom((z) => clampZoom(z - 0.25))}>
            <ZoomOut size={16} />
          </button>
          <button aria-label="zoom in" onClick={() => setZoom((z) => clampZoom(z + 0.25))}>
            <ZoomIn size={16} />
          </button>
        </div>
      )}
    </div>
  );
}
