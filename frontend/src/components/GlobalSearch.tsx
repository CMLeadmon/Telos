"use client";

import { useEffect, useState } from "react";
import { Search } from "lucide-react";
import { api } from "@/lib/api";

interface SearchResult {
  id: string;
  title: string;
  type: string;
}

export function GlobalSearch() {
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<SearchResult[]>([]);
  const [announcement, setAnnouncement] = useState("");

  useEffect(() => {
    if (query.trim().length < 2) {
      return;
    }

    const delay = setTimeout(() => {
      api<{ items?: SearchResult[] }>(`/api/v1/search?q=${encodeURIComponent(query)}`)
        .then((res) => {
          const items = res.items ?? [];
          setResults(items);
          setAnnouncement(`${items.length} search results found for ${query}`);
        })
        .catch(() => {
          setResults([]);
          setAnnouncement("Search request failed.");
        });
    }, 250);

    return () => clearTimeout(delay);
  }, [query]);

  const handleQueryChange = (val: string) => {
    setQuery(val);
    if (val.trim().length < 2) {
      setResults([]);
      setAnnouncement("");
    }
  };

  return (
    <div
      className="global-search"
      role="combobox"
      aria-expanded={results.length > 0}
      aria-haspopup="listbox"
      aria-controls="global-search-list"
    >
      <div className="search-input-wrapper">
        <Search size={16} />
        <input
          type="text"
          value={query}
          onChange={(e) => handleQueryChange(e.target.value)}
          placeholder="Search node..."
          aria-label="Search node"
          aria-autocomplete="list"
          aria-controls="global-search-list"
          data-testid="global-search-input"
        />
      </div>

      {/* Visually hidden live region for screen reader announcements */}
      <div className="sr-only" role="status" aria-live="polite" data-testid="search-announcement">
        {announcement}
      </div>

      {results.length > 0 && (
        <ul
          id="global-search-list"
          className="search-results-list"
          role="listbox"
          data-testid="global-search-results"
        >
          {results.map((r, i) => (
            <li key={r.id || i} role="option" aria-selected="false" tabIndex={0}>
              {r.title} ({r.type})
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
