import { Share2 } from "lucide-react";
import type { MediaItem } from "@/stores/useMediaStore";
import { assetUrl } from "@/lib/api";
import { DARK_TEXT, posterClass, posterMeta } from "./poster";

interface MediaShelfProps {
  title: string;
  items: MediaItem[];
  onSelectItem: (item: MediaItem) => void;
}

// Continue and Recently Added. Both are omitted when empty, per the design's
// shelf vocabulary, and both render the same poster card the browse grid uses
// so a title looks identical wherever a member meets it.
export function MediaShelf({ title, items, onSelectItem }: MediaShelfProps) {
  if (items.length === 0) return null;

  return (
    <section className="row" data-testid={`shelf-${title.toLowerCase().replace(/\s+/g, "-")}`}>
      <div className="rowhead">
        <h2>{title}</h2>
        <span className="more">
          {items.length} item{items.length === 1 ? "" : "s"}
        </span>
      </div>
      <div className="posters">
        {items.map((item) => {
          const cls = posterClass(item.id);
          return (
            <div key={item.id} className="posterwrap">
              <button
                className={`poster ${cls}${DARK_TEXT.has(cls) ? " pdark" : ""}`}
                data-testid="poster-leaf"
                onClick={() => onSelectItem(item)}
                aria-label={`open ${item.title}`}
              >
                {item.coverUrl ? (
                  // eslint-disable-next-line @next/next/no-img-element
                  <img className="pcover" src={assetUrl(item.coverUrl)} alt="" aria-hidden="true" />
                ) : (
                  <div className="motif" />
                )}
                <span className="pt">{item.title}</span>
                <span className="pm">{posterMeta(item)}</span>
              </button>
              <a
                className="poster-share"
                href={`/chat?share_kind=stream_film&share_ref=${encodeURIComponent(item.id)}`}
                title="Share to chat"
                aria-label={`share ${item.title} to chat`}
              >
                <Share2 size={13} />
              </a>
            </div>
          );
        })}
      </div>
    </section>
  );
}
