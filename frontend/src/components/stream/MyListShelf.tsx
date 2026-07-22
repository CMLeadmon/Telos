"use client";

import { useEffect } from "react";
import { useMyListStore } from "@/stores/useMyListStore";

// MyListShelf renders the viewer's ordered My List with remove and explicit
// move-before controls. Entries whose upstream item is gone stay in the list,
// marked unavailable, and remain individually removable.
export function MyListShelf() {
  const entries = useMyListStore((s) => s.entries);
  const status = useMyListStore((s) => s.status);
  const conflict = useMyListStore((s) => s.conflict);
  const nextCursor = useMyListStore((s) => s.nextCursor);
  const load = useMyListStore((s) => s.load);
  const loadMore = useMyListStore((s) => s.loadMore);
  const remove = useMyListStore((s) => s.remove);
  const moveBefore = useMyListStore((s) => s.moveBefore);

  useEffect(() => {
    void load();
  }, [load]);

  if (status !== "loading" && entries.length === 0) return null;

  return (
    <section className="mylist-shelf" data-testid="mylist-shelf" aria-label="My List">
      <h2 className="shelf-title">My List</h2>
      {conflict && (
        <p className="mylist-conflict" role="status">
          Your list changed elsewhere and was reloaded.
        </p>
      )}
      <ul className="mylist-row">
        {entries.map((e, i) => (
          <li
            key={e.itemId}
            className={`mylist-item${e.available ? "" : " unavailable"}`}
            data-testid={`mylist-item-${e.itemId}`}
          >
            <span className="mylist-title">{e.title || e.itemId}</span>
            {!e.available && <span className="mylist-badge">Unavailable</span>}
            <div className="mylist-controls">
              {i > 0 && (
                <button
                  aria-label={`move ${e.title} earlier`}
                  onClick={() => void moveBefore(e.itemId, entries[i - 1].itemId)}
                >
                  ←
                </button>
              )}
              <button
                aria-label={`remove ${e.title}`}
                data-testid={`mylist-remove-${e.itemId}`}
                onClick={() => void remove(e.itemId)}
              >
                ×
              </button>
            </div>
          </li>
        ))}
      </ul>
      {nextCursor && (
        <button className="mylist-more" onClick={() => void loadMore()} disabled={status === "loading"}>
          {status === "loading" ? "Loading…" : "Load more"}
        </button>
      )}
    </section>
  );
}
