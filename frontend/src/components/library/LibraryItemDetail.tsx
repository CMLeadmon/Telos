import { useEffect } from "react";
import { X } from "lucide-react";
import type { LibraryItem } from "@/stores/useLibraryStore";
import { assetUrl } from "@/lib/api";

interface LibraryItemDetailProps {
  item: LibraryItem | null;
  onClose: () => void;
  onOpenItem: (item: LibraryItem) => void;
}

function hours(durationMs?: number): string | null {
  if (!durationMs) return null;
  const h = durationMs / 3600000;
  return h < 1 ? `${Math.round(durationMs / 60000)} min` : `${h.toFixed(1)} h`;
}

export function LibraryItemDetail({ item, onClose, onOpenItem }: LibraryItemDetailProps) {
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [onClose]);

  if (!item) return null;

  const percent = Math.round((item.progress?.percent ?? 0) * 100);
  const duration = hours(item.durationMs);
  const series = item.seriesName
    ? `${item.seriesName}${item.seriesNumber ? ` #${item.seriesNumber}` : ""}`
    : null;

  // Continue only when there is somewhere to continue to — a completed item
  // reads as Read/Listen again, not as 100% progress.
  const action =
    percent > 0 && !item.progress?.completed
      ? `Continue (${percent}%)`
      : item.kind === "audiobook"
        ? "Listen"
        : "Read";

  return (
    <>
      <div className="idetail-scrim" onClick={onClose} />
      <div
        className="idetail"
        role="dialog"
        aria-modal="true"
        aria-labelledby="library-detail-title"
        data-testid="library-item-detail"
      >
        <div className="idetail-head">
          <div>
            <div className="idetail-kicker">
              {item.kind}
              {series ? ` · ${series}` : ""}
            </div>
            <h2 id="library-detail-title">{item.title}</h2>
            {item.subtitle && <p className="idetail-sub">{item.subtitle}</p>}
          </div>
          <button className="iconbtn" onClick={onClose} aria-label="close details">
            <X size={16} />
          </button>
        </div>

        <div className="idetail-body">
          <div className="idetail-cover">
            {item.coverUrl ? (
              // eslint-disable-next-line @next/next/no-img-element
              <img src={assetUrl(item.coverUrl)} alt="" aria-hidden="true" />
            ) : (
              <span className="nocover">no cover</span>
            )}
          </div>

          <div className="idetail-meta">
            <p className="idetail-byline">
              {item.authors && item.authors.length > 0
                ? item.authors.join(", ")
                : "Unknown author"}
            </p>

            <div className="idetail-facts">
              {item.narrator && <span>read by {item.narrator}</span>}
              {duration && <span>{duration}</span>}
              {item.publishedDate && <span>{item.publishedDate}</span>}
              {item.language && <span>{item.language}</span>}
            </div>

            {item.description && <p className="idetail-desc">{item.description}</p>}

            {item.categories && item.categories.length > 0 && (
              <div className="idetail-tags">
                {item.categories.map((cat) => (
                  <span className="idetail-tag" key={cat}>
                    {cat}
                  </span>
                ))}
              </div>
            )}

            {percent > 0 && (
              <div className="idetail-progress">
                <div className="plabel">
                  <span>progress</span>
                  <span>{percent}%</span>
                </div>
                <div className="idetail-track">
                  <span style={{ width: `${percent}%` }} />
                </div>
              </div>
            )}
          </div>
        </div>

        <div className="idetail-foot">
          <button className="btn" onClick={() => onOpenItem(item)}>
            {action}
          </button>
          <a
            className="btn-ghost"
            href={`/chat?share_kind=library_book&share_ref=${encodeURIComponent(item.id)}`}
          >
            Share to chat
          </a>
          <span className="grow" />
          <button className="btn-ghost" onClick={onClose}>
            Cancel
          </button>
        </div>
      </div>
    </>
  );
}
