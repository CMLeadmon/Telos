import type { MediaItem } from "@/stores/useMediaStore";

// Poster presentation shared by the browse grid and the Continue/Recent
// shelves. Both surfaces show the same card, so the motif palette and meta
// line live here rather than being reimplemented per shelf.

const POSTER_CLASSES = ["c0", "c1", "c2", "c3", "c4", "c5", "c6", "c7"];

// The two light motifs need dark ink over them.
export const DARK_TEXT = new Set(["c2", "c6"]);

// A deterministic placeholder for items Jellyfin has no artwork for. Keyed by
// ID so a given item keeps its colour across sessions and surfaces.
export function posterClass(id: string): string {
  let h = 0;
  for (const ch of id) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  return POSTER_CLASSES[h % POSTER_CLASSES.length];
}

// A folder's meta line describes what's inside instead of a duration —
// folders (series, seasons, audiobooks) carry no runtime of their own.
function folderNoun(type: string): string {
  switch (type) {
    case "Series":
      return "season";
    case "Season":
      return "episode";
    default:
      return "item";
  }
}

export function posterMeta(item: MediaItem): string {
  if (item.isFolder) {
    const count = item.childCount ?? 0;
    const noun = folderNoun(item.type);
    return `${count} ${noun}${count === 1 ? "" : "s"}`;
  }
  return [item.duration, item.type.toLowerCase()].filter(Boolean).join(" · ");
}
