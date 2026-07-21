// Cursor-pagination helpers for the client. The server returns opaque
// `nextCursor` tokens; the client never parses or reuses them across scopes,
// and merges pages by stable id.

export interface Page<T> {
  items: T[];
  nextCursor?: string;
}

/** Build a query string for the next page. */
export function pageQuery(cursor?: string, limit?: number): string {
  const params = new URLSearchParams();
  if (cursor) params.set("cursor", cursor);
  if (limit) params.set("limit", String(limit));
  const q = params.toString();
  return q ? `?${q}` : "";
}

/**
 * Merge a freshly fetched page into an accumulated list, de-duplicating by id
 * so an item that shifts between pages (concurrent insert/delete) is not
 * duplicated.
 */
export function mergePage<T extends { id: string }>(existing: T[], page: Page<T>): T[] {
  const seen = new Set(existing.map((x) => x.id));
  const merged = existing.slice();
  for (const item of page.items) {
    if (!seen.has(item.id)) {
      seen.add(item.id);
      merged.push(item);
    }
  }
  return merged;
}
