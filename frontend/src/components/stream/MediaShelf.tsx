import React from "react";
import { Play } from "lucide-react";
import { MediaItem } from "@/stores/useMediaStore";

interface MediaShelfProps {
  title: string;
  items: MediaItem[];
  onSelectItem: (item: MediaItem) => void;
}

export function MediaShelf({ title, items, onSelectItem }: MediaShelfProps) {
  if (items.length === 0) return null;

  return (
    <section className="space-y-3 py-2">
      <h2 className="text-xs font-mono text-emerald-400 tracking-wider uppercase">{`// ${title}`}</h2>
      <div className="flex gap-4 overflow-x-auto pb-4 scrollbar-thin scrollbar-thumb-neutral-800">
        {items.map((item) => (
          <article
            key={item.id}
            onClick={() => onSelectItem(item)}
            className="group relative flex-none w-48 rounded-2xl bg-neutral-900/60 border border-neutral-800/80 p-3 cursor-pointer hover:border-emerald-500/50 hover:bg-neutral-900 transition-all duration-200"
            role="button"
            tabIndex={0}
            aria-label={`Open ${item.title}`}
          >
            <div className="aspect-video w-full rounded-xl bg-neutral-950 flex items-center justify-center border border-neutral-800/50 relative overflow-hidden">
              <span className="text-3xl group-hover:scale-110 transition-transform">🎬</span>
              <div className="absolute inset-0 bg-black/40 opacity-0 group-hover:opacity-100 flex items-center justify-center transition-opacity">
                <div className="h-10 w-10 rounded-full bg-emerald-500 text-neutral-950 flex items-center justify-center shadow-lg">
                  <Play size={18} fill="currentColor" />
                </div>
              </div>
            </div>
            <div className="mt-2.5 space-y-0.5">
              <h3 className="text-xs font-semibold text-white truncate">{item.title}</h3>
              <p className="text-[10px] font-mono text-neutral-400 capitalize">{item.type || "video"}</p>
            </div>
          </article>
        ))}
      </div>
    </section>
  );
}
