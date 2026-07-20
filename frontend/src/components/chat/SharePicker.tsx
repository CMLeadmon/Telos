"use client";

import React, { useState, useEffect } from "react";
import { Search, X, Book, Film } from "lucide-react";
import { api } from "@/lib/api";

interface SharePickerProps {
  onClose: () => void;
  onPick: (kind: "library_book" | "stream_film", ref: string, title: string) => void;
  defaultTab?: "book" | "film";
}

interface PickerItem {
  id: string;
  title: string;
  subtitle: string;
  kind: "library_book" | "stream_film";
}

interface PickerBook {
  id: number;
  title: string;
  authors: string[];
}

interface PickerLibrary {
  id: string;
  name: string;
  type: "video" | "audio";
}

interface PickerMediaItem {
  id: string;
  title: string;
  duration: string;
  isFolder: boolean;
}

export function SharePicker({ onClose, onPick, defaultTab }: SharePickerProps) {
  const [tab, setTab] = useState<"book" | "film">(defaultTab || "book");
  const [search, setSearch] = useState("");
  const [books, setBooks] = useState<PickerItem[]>([]);
  const [films, setFilms] = useState<PickerItem[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    // Fetch library books
    api<PickerBook[]>("/api/v1/library/books")
      .then((res) => {
        const mapped = (res || []).map((b) => ({
          id: String(b.id),
          title: b.title,
          subtitle: (b.authors || []).join(", "),
          kind: "library_book" as const,
        }));
        setBooks(mapped);
      })
      .catch(() => {});

    // Fetch media libraries, then fetch items under each parent
    api<PickerLibrary[]>("/api/v1/media")
      .then(async (libs) => {
        const allItems: PickerItem[] = [];
        for (const lib of libs || []) {
          try {
            const items = await api<PickerMediaItem[]>(`/api/v1/media/items?parentId=${lib.id}`);
            for (const item of items || []) {
              if (!item.isFolder) {
                allItems.push({
                  id: item.id,
                  title: item.title,
                  subtitle: item.duration || "",
                  kind: "stream_film" as const,
                });
              }
            }
          } catch {
            // ignore fetch errors for specific library
          }
        }
        setFilms(allItems);
      })
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []);

  const activeItems = tab === "book" ? books : films;
  const filtered = activeItems.filter(
    (item) =>
      item.title.toLowerCase().includes(search.toLowerCase()) ||
      item.subtitle.toLowerCase().includes(search.toLowerCase())
  );

  return (
    <>
      <div
        style={{
          position: "fixed",
          inset: 0,
          background: "rgba(0, 0, 0, 0.6)",
          backdropFilter: "blur(4px)",
          zIndex: 1100,
        }}
        onClick={onClose}
      />
      <div
        className="share-picker"
        style={{
          position: "fixed",
          top: "50%",
          left: "50%",
          transform: "translate(-50%, -50%)",
          width: "480px",
          height: "400px",
          background: "var(--surface-2)",
          border: "1px solid var(--line)",
          borderRadius: "var(--r)",
          display: "flex",
          flexDirection: "column",
          zIndex: 1105,
          overflow: "hidden",
          boxShadow: "0 10px 30px rgba(0, 0, 0, 0.4)",
        }}
      >
        <div
          style={{
            padding: "12px 16px",
            borderBottom: "1px solid var(--line-2)",
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            background: "var(--surface)",
          }}
        >
          <span style={{ fontSize: "14px", fontWeight: 600 }}>Share Content</span>
          <button
            onClick={onClose}
            style={{
              background: "transparent",
              border: "none",
              cursor: "pointer",
              color: "var(--faint)",
            }}
          >
            <X size={18} />
          </button>
        </div>

        <div
          style={{
            display: "flex",
            borderBottom: "1px solid var(--line-2)",
          }}
        >
          <button
            onClick={() => setTab("book")}
            style={{
              flex: 1,
              padding: "10px",
              background: tab === "book" ? "var(--surface-3)" : "transparent",
              border: "none",
              cursor: "pointer",
              fontWeight: 600,
              fontSize: "12.5px",
              color: tab === "book" ? "var(--accent)" : "var(--ink)",
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              gap: "8px",
            }}
          >
            <Book size={14} />
            Library Books
          </button>
          <button
            onClick={() => setTab("film")}
            style={{
              flex: 1,
              padding: "10px",
              background: tab === "film" ? "var(--surface-3)" : "transparent",
              border: "none",
              cursor: "pointer",
              fontWeight: 600,
              fontSize: "12.5px",
              color: tab === "film" ? "var(--accent)" : "var(--ink)",
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              gap: "8px",
            }}
          >
            <Film size={14} />
            Stream Media
          </button>
        </div>

        <div
          style={{
            padding: "10px 16px",
            borderBottom: "1px solid var(--line-2)",
            display: "flex",
            alignItems: "center",
            gap: "8px",
          }}
        >
          <Search size={14} style={{ color: "var(--faint)" }} />
          <input
            type="text"
            placeholder="Search catalog..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            style={{
              flex: 1,
              background: "transparent",
              border: "none",
              outline: "none",
              color: "var(--ink)",
              fontSize: "13px",
            }}
          />
        </div>

        <div
          style={{
            flex: 1,
            overflowY: "auto",
            padding: "8px",
            display: "flex",
            flexDirection: "column",
            gap: "4px",
          }}
        >
          {loading ? (
            <div
              style={{
                padding: "40px",
                textAlign: "center",
                color: "var(--faint)",
                fontSize: "13px",
              }}
            >
              Loading items...
            </div>
          ) : filtered.length === 0 ? (
            <div
              style={{
                padding: "40px",
                textAlign: "center",
                color: "var(--faint)",
                fontSize: "13px",
              }}
            >
              No items found
            </div>
          ) : (
            filtered.map((item) => (
              <button
                key={item.id}
                onClick={() => {
                  onPick(item.kind, item.id, item.title);
                  onClose();
                }}
                style={{
                  display: "flex",
                  flexDirection: "column",
                  alignItems: "flex-start",
                  padding: "8px 12px",
                  border: "none",
                  background: "transparent",
                  cursor: "pointer",
                  borderRadius: "var(--r-xs)",
                  textAlign: "left",
                  width: "100%",
                }}
                className="picker-item-btn"
              >
                <div style={{ fontSize: "13px", fontWeight: 600, color: "var(--ink)" }}>
                  {item.title}
                </div>
                <div style={{ fontSize: "11px", color: "var(--faint)" }}>{item.subtitle}</div>
              </button>
            ))
          )}
        </div>
      </div>
    </>
  );
}
