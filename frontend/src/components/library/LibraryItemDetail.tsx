import React, { useEffect, useRef } from "react";
import { LibraryItem } from "../../stores/useLibraryStore";

interface LibraryItemDetailProps {
  item: LibraryItem | null;
  onClose: () => void;
  onOpenItem: (item: LibraryItem) => void;
}

export function LibraryItemDetail({ item, onClose, onOpenItem }: LibraryItemDetailProps) {
  const modalRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        onClose();
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [onClose]);

  if (!item) return null;

  const durationHours = item.durationMs ? (item.durationMs / 3600000).toFixed(1) : null;
  const percentDisplay = item.progress?.percent ? Math.round(item.progress.percent * 100) : 0;

  const getActionLabel = () => {
    if (item.progress && item.progress.percent > 0 && !item.progress.completed) {
      return `Continue (${percentDisplay}%)`;
    }
    return item.kind === "audiobook" ? "Listen" : "Read";
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/75 p-4 backdrop-blur-sm"
      role="dialog"
      aria-modal="true"
      aria-labelledby="detail-title"
    >
      <div
        ref={modalRef}
        className="relative flex w-full max-w-2xl flex-col md:flex-row gap-6 rounded-2xl bg-neutral-900 p-6 text-neutral-100 shadow-2xl border border-neutral-800"
      >
        <button
          onClick={onClose}
          className="absolute top-4 right-4 text-neutral-400 hover:text-white transition-colors"
          aria-label="Close details"
        >
          ✕
        </button>

        {/* Cover Art */}
        <div className="w-full md:w-48 shrink-0 aspect-[2/3] rounded-lg overflow-hidden bg-neutral-800 border border-neutral-700 shadow-md">
          {item.coverUrl ? (
            <img src={item.coverUrl} alt={item.title} className="h-full w-full object-cover" />
          ) : (
            <div className="flex h-full w-full items-center justify-center text-neutral-500 font-semibold">
              No Cover
            </div>
          )}
        </div>

        {/* Details Content */}
        <div className="flex flex-1 flex-col justify-between space-y-4">
          <div>
            <div className="flex items-center gap-2 text-xs font-semibold uppercase tracking-wider text-amber-400">
              <span>{item.kind}</span>
              {item.seriesName && (
                <>
                  <span>•</span>
                  <span>
                    {item.seriesName} {item.seriesNumber ? `#${item.seriesNumber}` : ""}
                  </span>
                </>
              )}
            </div>

            <h2 id="detail-title" className="mt-1 text-2xl font-bold text-white leading-tight">
              {item.title}
            </h2>
            {item.subtitle && <p className="text-sm text-neutral-400 italic mt-0.5">{item.subtitle}</p>}

            <p className="mt-2 text-sm font-medium text-neutral-300">
              {item.authors && item.authors.length > 0 ? item.authors.join(", ") : "Unknown Author"}
            </p>
            {item.narrator && (
              <p className="text-xs text-neutral-400">Narrated by: {item.narrator}</p>
            )}

            {durationHours && (
              <p className="mt-1 text-xs text-neutral-400">Duration: {durationHours} hours</p>
            )}

            <p className="mt-4 text-sm text-neutral-300 line-clamp-4 leading-relaxed">
              {item.description || "No description available."}
            </p>

            {item.categories && item.categories.length > 0 && (
              <div className="mt-3 flex flex-wrap gap-1.5">
                {item.categories.map((cat, idx) => (
                  <span
                    key={idx}
                    className="rounded-full bg-neutral-800 px-2.5 py-0.5 text-xs text-neutral-300 border border-neutral-700"
                  >
                    {cat}
                  </span>
                ))}
              </div>
            )}
          </div>

          {/* Action Bar & Progress */}
          <div className="pt-4 border-t border-neutral-800 space-y-3">
            {percentDisplay > 0 && (
              <div className="space-y-1">
                <div className="flex justify-between text-xs text-neutral-400">
                  <span>Progress</span>
                  <span>{percentDisplay}%</span>
                </div>
                <div className="h-1.5 w-full rounded-full bg-neutral-800 overflow-hidden">
                  <div
                    className="h-full bg-amber-500 rounded-full transition-all duration-300"
                    style={{ width: `${percentDisplay}%` }}
                  />
                </div>
              </div>
            )}

            <div className="flex gap-3">
              <button
                onClick={() => onOpenItem(item)}
                className="flex-1 rounded-xl bg-amber-500 px-5 py-2.5 font-semibold text-neutral-950 hover:bg-amber-400 transition-colors shadow-lg shadow-amber-500/10"
              >
                {getActionLabel()}
              </button>
              <button
                onClick={onClose}
                className="rounded-xl bg-neutral-800 px-4 py-2.5 font-medium text-neutral-300 hover:bg-neutral-700 transition-colors"
              >
                Cancel
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
