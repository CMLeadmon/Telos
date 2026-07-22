// Format-specific reader locators and progress helpers. Kept pure so they can be
// unit-tested without loading epub.js or PDF.js.

export type ReaderLocator =
  | { kind: "epub"; cfi: string; fraction: number }
  | { kind: "pdf"; page: number; zoom: number };

export type BookFormat = "EPUB" | "PDF";

// formatKind maps a server-reported book format to a reader kind, or null when
// the format is not readable in-app.
export function formatKind(format: string): "epub" | "pdf" | null {
  switch ((format || "").toUpperCase()) {
    case "EPUB":
      return "epub";
    case "PDF":
      return "pdf";
    default:
      return null;
  }
}

export const ZOOM_MIN = 0.1;
export const ZOOM_MAX = 10;
export const ZOOM_DEFAULT = 1;

// clampZoom keeps a zoom finite and within the server-accepted [0.1, 10] range.
export function clampZoom(z: number): number {
  if (!Number.isFinite(z)) return ZOOM_DEFAULT;
  return Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, z));
}

// clampPage keeps a page a finite integer within [1, total].
export function clampPage(page: number, total: number): number {
  if (!Number.isFinite(page)) return 1;
  const p = Math.trunc(page);
  if (p < 1) return 1;
  if (total > 0 && p > total) return total;
  return p;
}

// epubProgressPayload builds the PUT body for an EPUB position.
export function epubProgressPayload(cfi: string, fraction: number) {
  const f = Number.isFinite(fraction) ? Math.min(1, Math.max(0, fraction)) : 0;
  return { locator: { cfi, fraction: f }, percent: f };
}

// pdfProgressPayload builds the PUT body for a PDF position; percent is the
// page fraction through the document.
export function pdfProgressPayload(page: number, zoom: number, totalPages: number) {
  const p = clampPage(page, totalPages);
  const z = clampZoom(zoom);
  const percent = totalPages > 0 ? Math.min(1, Math.max(0, p / totalPages)) : 0;
  return { locator: { page: p, zoom: z }, percent };
}

// parseStoredLocator validates a stored locator for a format, returning a typed
// ReaderLocator or null when it is missing/incoherent (so the reader falls back
// to the start rather than a corrupt position).
export function parseStoredLocator(format: string, locator: unknown): ReaderLocator | null {
  const kind = formatKind(format);
  if (!kind || typeof locator !== "object" || locator === null) return null;
  const l = locator as Record<string, unknown>;
  if (kind === "epub") {
    const cfi = l.cfi;
    const fraction = l.fraction;
    if (typeof cfi === "string" && cfi.trim() !== "") {
      return { kind: "epub", cfi, fraction: typeof fraction === "number" ? fraction : 0 };
    }
    return null;
  }
  // pdf
  const page = l.page;
  const zoom = l.zoom;
  if (typeof page === "number" && Number.isFinite(page) && page >= 1) {
    return { kind: "pdf", page: Math.trunc(page), zoom: clampZoom(typeof zoom === "number" ? zoom : ZOOM_DEFAULT) };
  }
  return null;
}
