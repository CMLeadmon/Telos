"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Book,
  ChevronRight,
  File as FileIcon,
  Film,
  Folder,
  Search,
  X,
} from "lucide-react";
import { api } from "@/lib/api";
import { useAuthStore } from "@/stores/useAuthStore";
import { hasCapability } from "@/lib/capabilities";

export type ShareKind = "library_book" | "stream_film" | "file";
export type ShareTab = "book" | "film" | "file";

interface SharePickerProps {
  onClose: () => void;
  onPick: (kind: ShareKind, ref: string, title: string) => void;
  defaultTab?: ShareTab;
}

// --- wire shapes -----------------------------------------------------------
// The two file surfaces disagree on casing: the browser (/api/v1/files) speaks
// snake_case off the filesystem, search (/api/v1/search) speaks camelCase and
// covers both the files table and the volume. Either ID is shareable — see
// resolveSharedFileMeta on the gateway.

interface WireBook {
  // Canonical Telos catalog ID. Books were numeric Grimmory IDs before the
  // catalog-identity cutover; the gateway now returns a UUID here.
  id: string;
  title: string;
  authors: string[] | null;
}
interface WireLibrary {
  id: string;
  name: string;
}
interface WireMediaItem {
  id: string;
  title: string;
  duration: string;
  type: string;
  isFolder: boolean;
  childCount?: number;
}
interface WireFolder {
  id: string;
  name: string;
  path: string;
}
interface WireBrowseFile {
  id: string;
  filename: string;
  size_bytes: number;
  mime_type: string;
}
interface WireListing {
  folders: WireFolder[];
  files: WireBrowseFile[];
  hasNext: boolean;
}
interface WireSearchFile {
  id: string;
  filename: string;
  sizeBytes: number;
  mimeType: string;
}
interface WireSearch {
  books: WireBook[];
  media: WireMediaItem[];
  files: WireSearchFile[];
}

// A row is either somewhere to go or something to share. Folders are never
// pickable: the gateway resolves an embed to a concrete file or media item, so
// a folder ref would be refused.
type Row =
  | { row: "folder"; id: string; title: string; subtitle: string; go: () => void }
  | { row: "leaf"; id: string; kind: ShareKind; ref: string; title: string; subtitle: string };

const TABS: { id: ShareTab; label: string; capability: string; icon: typeof Book }[] = [
  { id: "book", label: "Library", capability: "view_library", icon: Book },
  { id: "film", label: "Stream", capability: "view_media", icon: Film },
  { id: "file", label: "Files", capability: "view_files", icon: FileIcon },
];

// The gateway ignores terms shorter than this (normalizedSearchTerm), so below
// it we stay in browse rather than firing a request that returns nothing.
const MIN_QUERY = 2;

function humanSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KiB", "MiB", "GiB"];
  let v = bytes / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v >= 10 ? Math.round(v) : v.toFixed(1)} ${units[i]}`;
}

function mediaSubtitle(item: WireMediaItem): string {
  if (item.isFolder) {
    const n = item.childCount ?? 0;
    return `${n} item${n === 1 ? "" : "s"}`;
  }
  return [item.duration, item.type.toLowerCase()].filter(Boolean).join(" · ");
}

export function SharePicker({ onClose, onPick, defaultTab }: SharePickerProps) {
  const user = useAuthStore((s) => s.user);
  const tabs = useMemo(
    () => TABS.filter((t) => hasCapability(user, t.capability)),
    [user],
  );

  const [tab, setTab] = useState<ShareTab>(defaultTab ?? "book");
  const [query, setQuery] = useState("");
  const [term, setTerm] = useState("");

  // Browse position. Media walks a Jellyfin trail (library → series → season);
  // an empty trail means the library list itself. Files walk a path.
  const [trail, setTrail] = useState<{ id: string; name: string }[]>([]);
  const [path, setPath] = useState("");
  const [filePage, setFilePage] = useState(1);

  // null means "not loaded yet", which is what drives the loading state — this
  // component may not call setState synchronously inside an effect, so loading
  // is derived rather than tracked.
  const [books, setBooks] = useState<WireBook[] | null>(null);
  const [media, setMedia] = useState<WireMediaItem[] | null>(null);
  const [listing, setListing] = useState<WireListing | null>(null);
  const [results, setResults] = useState<WireSearch | null>(null);

  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const searching = term.length >= MIN_QUERY;
  const parentId = trail.length ? trail[trail.length - 1].id : null;

  // If the caller asked for a tab this user cannot see, fall back to the first
  // one they can.
  const activeTab = tabs.some((t) => t.id === tab) ? tab : tabs[0]?.id;

  useEffect(() => {
    const t = setTimeout(() => {
      setTerm(query.trim());
      setResults(null);
      setError(null);
    }, 250);
    return () => clearTimeout(t);
  }, [query]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  // One search covers every tab, so switching tabs while a query is active is
  // free — no refetch.
  useEffect(() => {
    if (!searching) return;
    let stale = false;
    api<WireSearch>(`/api/v1/search?q=${encodeURIComponent(term)}`)
      .then((res) => {
        if (!stale) setResults(res);
      })
      .catch(() => {
        if (!stale) setError("Search is unavailable right now.");
      });
    return () => {
      stale = true;
    };
  }, [term, searching]);

  useEffect(() => {
    if (searching || activeTab !== "book" || books !== null) return;
    let stale = false;
    api<WireBook[]>("/api/v1/library/books")
      .then((res) => {
        if (!stale) setBooks(res ?? []);
      })
      .catch(() => {
        if (!stale) setError("The catalog is unavailable right now.");
      });
    return () => {
      stale = true;
    };
  }, [searching, activeTab, books]);

  // Fetches only the level being viewed. The previous picker walked every
  // library on mount and then discarded the folders, which is why nothing
  // nested was ever shareable.
  useEffect(() => {
    if (searching || activeTab !== "film" || media !== null) return;
    let stale = false;
    const request = parentId
      ? api<WireMediaItem[]>(
          `/api/v1/media/items?parentId=${encodeURIComponent(parentId)}`,
        )
      : api<WireLibrary[]>("/api/v1/media").then((libs) =>
          (libs ?? []).map((l) => ({
            id: l.id,
            title: l.name,
            duration: "",
            type: "Library",
            isFolder: true,
          })),
        );
    request
      .then((items) => {
        if (!stale) setMedia(items ?? []);
      })
      .catch(() => {
        if (!stale) setError("The stream library is unavailable right now.");
      });
    return () => {
      stale = true;
    };
  }, [searching, activeTab, parentId, media]);

  useEffect(() => {
    if (searching || activeTab !== "file") return;
    if (filePage === 1 && listing !== null) return;
    let stale = false;
    api<WireListing>(
      `/api/v1/files?path=${encodeURIComponent(path)}&page=${filePage}`,
    )
      .then((res) => {
        if (stale) return;
        // Paging appends: to the picker the shelf is one list, not pages.
        setListing((prev) =>
          filePage > 1 && prev
            ? {
                folders: [...prev.folders, ...(res.folders ?? [])],
                files: [...prev.files, ...(res.files ?? [])],
                hasNext: res.hasNext,
              }
            : {
                folders: res.folders ?? [],
                files: res.files ?? [],
                hasNext: res.hasNext,
              },
        );
      })
      .catch(() => {
        if (!stale) setError("The shelf is unavailable right now.");
      })
      .finally(() => {
        if (!stale) setLoadingMore(false);
      });
    return () => {
      stale = true;
    };
  }, [searching, activeTab, path, filePage, listing]);

  const goMedia = useCallback((next: { id: string; name: string }[]) => {
    setQuery("");
    setTerm("");
    setError(null);
    setMedia(null);
    setTrail(next);
  }, []);

  const goPath = useCallback((next: string) => {
    setQuery("");
    setTerm("");
    setError(null);
    setListing(null);
    setFilePage(1);
    setPath(next);
  }, []);

  const rows: Row[] = useMemo(() => {
    const bookRow = (b: WireBook): Row => ({
      row: "leaf",
      id: `book-${b.id}`,
      kind: "library_book",
      ref: b.id,
      title: b.title,
      subtitle: (b.authors ?? []).join(", "),
    });

    const mediaRow = (item: WireMediaItem): Row =>
      item.isFolder
        ? {
            row: "folder",
            id: `media-${item.id}`,
            title: item.title,
            subtitle: mediaSubtitle(item),
            go: () => goMedia([...trail, { id: item.id, name: item.title }]),
          }
        : {
            row: "leaf",
            id: `media-${item.id}`,
            kind: "stream_film",
            ref: item.id,
            title: item.title,
            subtitle: mediaSubtitle(item),
          };

    if (searching) {
      if (!results) return [];
      if (activeTab === "book") return (results.books ?? []).map(bookRow);
      if (activeTab === "film") return (results.media ?? []).map(mediaRow);
      return (results.files ?? []).map((f) => ({
        row: "leaf" as const,
        id: `file-${f.id}`,
        kind: "file" as const,
        ref: f.id,
        title: f.filename,
        subtitle: `${humanSize(f.sizeBytes)} · ${f.mimeType}`,
      }));
    }

    if (activeTab === "book") return (books ?? []).map(bookRow);
    if (activeTab === "film") return (media ?? []).map(mediaRow);
    if (!listing) return [];
    return [
      ...listing.folders.map((f) => ({
        row: "folder" as const,
        id: `dir-${f.id}`,
        title: f.name,
        subtitle: "folder",
        go: () => goPath(f.path),
      })),
      ...listing.files.map((f) => ({
        row: "leaf" as const,
        id: `file-${f.id}`,
        kind: "file" as const,
        ref: f.id,
        title: f.filename,
        subtitle: `${humanSize(f.size_bytes)} · ${f.mime_type}`,
      })),
    ];
  }, [searching, results, activeTab, books, media, listing, trail, goMedia, goPath]);

  const slice = searching
    ? results
    : activeTab === "book"
      ? books
      : activeTab === "film"
        ? media
        : listing;
  const loading = !error && activeTab !== undefined && slice === null;

  const crumbs =
    searching || activeTab === "book"
      ? []
      : activeTab === "film"
        ? [
            { label: "Stream", go: () => goMedia([]) },
            ...trail.map((node, i) => ({
              label: node.name,
              go: () => goMedia(trail.slice(0, i + 1)),
            })),
          ]
        : activeTab === "file"
          ? [
              { label: "Files", go: () => goPath("") },
              ...(path ? path.split("/") : []).map((seg, i, all) => ({
                label: seg,
                go: () => goPath(all.slice(0, i + 1).join("/")),
              })),
            ]
          : [];

  const emptyMessage =
    activeTab === undefined
      ? "You do not have access to any shareable content."
      : searching
        ? `Nothing matches “${term}”.`
        : activeTab === "book"
          ? "The catalog is empty."
          : "Nothing here yet.";

  return (
    <>
      <div className="sp-scrim" onClick={onClose} />
      <div className="sp" role="dialog" aria-modal="true" aria-label="Share content">
        <div className="sp-head">
          <span className="sp-title">Share content</span>
          <button className="iconbtn" aria-label="close share picker" onClick={onClose}>
            <X size={16} />
          </button>
        </div>

        <div className="sp-tabs" role="tablist">
          {tabs.map((t) => {
            const Icon = t.icon;
            return (
              <button
                key={t.id}
                role="tab"
                aria-selected={activeTab === t.id}
                className={`sp-tab${activeTab === t.id ? " on" : ""}`}
                onClick={() => {
                  setError(null);
                  setTab(t.id);
                }}
              >
                <Icon size={14} />
                {t.label}
              </button>
            );
          })}
        </div>

        <div className="sp-search">
          <Search size={14} />
          <input
            autoFocus
            type="text"
            aria-label="search shareable content"
            placeholder="Search everything, or browse below…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
          {query && (
            <button className="iconbtn" aria-label="clear search" onClick={() => setQuery("")}>
              <X size={14} />
            </button>
          )}
        </div>

        {crumbs.length > 0 && (
          <div className="sp-crumbs" data-testid="share-picker-crumbs">
            {crumbs.map((c, i) => (
              <span key={`${c.label}-${i}`} className="sp-crumb-wrap">
                {i > 0 && <span className="sp-sep">/</span>}
                <button
                  className={`sp-crumb${i === crumbs.length - 1 ? " here" : ""}`}
                  onClick={c.go}
                >
                  {c.label}
                </button>
              </span>
            ))}
          </div>
        )}

        <div className="sp-list" data-testid="share-picker-list">
          {error ? (
            <p className="sp-note">{error}</p>
          ) : loading && rows.length === 0 ? (
            <p className="sp-note">Loading…</p>
          ) : rows.length === 0 ? (
            <p className="sp-note">{emptyMessage}</p>
          ) : (
            rows.map((r) =>
              r.row === "folder" ? (
                <button
                  key={r.id}
                  className="sp-row"
                  data-testid="share-picker-folder"
                  onClick={r.go}
                >
                  <span className="sp-icon">
                    <Folder size={15} />
                  </span>
                  <span className="sp-meta">
                    <span className="sp-name">{r.title}</span>
                    <span className="sp-sub">{r.subtitle}</span>
                  </span>
                  <ChevronRight size={15} className="sp-chev" />
                </button>
              ) : (
                <button
                  key={r.id}
                  className="sp-row"
                  data-testid="share-picker-leaf"
                  onClick={() => {
                    onPick(r.kind, r.ref, r.title);
                    onClose();
                  }}
                >
                  <span className="sp-icon">
                    {r.kind === "library_book" ? (
                      <Book size={15} />
                    ) : r.kind === "stream_film" ? (
                      <Film size={15} />
                    ) : (
                      <FileIcon size={15} />
                    )}
                  </span>
                  <span className="sp-meta">
                    <span className="sp-name">{r.title}</span>
                    <span className="sp-sub">{r.subtitle}</span>
                  </span>
                </button>
              ),
            )
          )}

          {!searching && activeTab === "file" && listing?.hasNext && (
            <button
              className="sp-more"
              disabled={loadingMore}
              onClick={() => {
                setLoadingMore(true);
                setFilePage((p) => p + 1);
              }}
            >
              {loadingMore ? "Loading…" : "Show more"}
            </button>
          )}
        </div>
      </div>
    </>
  );
}
