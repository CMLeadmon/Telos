// Pure helpers to turn a browser text selection over a PDF page element into
// normalized (0..1) highlight rectangles the backend accepts.

export interface NormRect {
  x: number;
  y: number;
  w: number;
  h: number;
}

interface BoxLike {
  left: number;
  top: number;
  width: number;
  height: number;
}

// normalizeRects converts client rectangles (from Range.getClientRects()) into
// page-relative normalized rectangles, dropping empty/degenerate ones and
// clamping to [0,1]. A rectangle wholly outside the page is discarded.
export function normalizeRects(pageBox: BoxLike, clientRects: BoxLike[]): NormRect[] {
  if (pageBox.width <= 0 || pageBox.height <= 0) return [];
  const out: NormRect[] = [];
  for (const r of clientRects) {
    if (r.width <= 0 || r.height <= 0) continue;
    const x = (r.left - pageBox.left) / pageBox.width;
    const y = (r.top - pageBox.top) / pageBox.height;
    const w = r.width / pageBox.width;
    const h = r.height / pageBox.height;
    // Skip rectangles entirely outside the page.
    if (x + w <= 0 || y + h <= 0 || x >= 1 || y >= 1) continue;
    const cx = clamp01(x);
    const cy = clamp01(y);
    out.push({
      x: cx,
      y: cy,
      w: Math.max(0, Math.min(1 - cx, w)),
      h: Math.max(0, Math.min(1 - cy, h)),
    });
  }
  return out.filter((r) => r.w > 0 && r.h > 0);
}

function clamp01(n: number): number {
  if (!Number.isFinite(n)) return 0;
  return Math.min(1, Math.max(0, n));
}

// selectionText returns the trimmed selected string, bounded to the backend's
// 2,000 code-point limit.
export function selectionText(raw: string): string {
  const trimmed = raw.trim();
  const points = Array.from(trimmed);
  return points.length > 2000 ? points.slice(0, 2000).join("") : trimmed;
}
