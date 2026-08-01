import { create } from "zustand";
import { api } from "@/lib/api";

export type LibraryKind = "epub" | "pdf" | "audiobook";

export interface MemberProgress {
  locator?: Record<string, unknown>;
  positionMs?: number;
  durationMs?: number;
  percent?: number;
  completed?: boolean;
}

export interface LibraryItem {
  id: string;
  title: string;
  subtitle?: string;
  authors: string[] | null;
  narrator?: string;
  categories: string[] | null;
  language?: string;
  description?: string;
  seriesName?: string;
  seriesNumber?: number | null;
  publishedDate?: string;
  addedOn?: string;
  kind: LibraryKind;
  format?: string;
  durationMs?: number;
  coverUrl?: string;
  progress?: MemberProgress;
}

export type LibraryBook = LibraryItem;

export interface LibraryAuthor {
  id: string;
  name: string;
  bookCount: number;
}

export interface LibrarySeries {
  name: string;
  bookCount: number;
}

export interface FacetValue {
  value: string;
  count: number;
}

export interface LibraryFacets {
  authors: FacetValue[] | null;
  categories: FacetValue[] | null;
  languages: FacetValue[] | null;
  formats: FacetValue[] | null;
}

export interface LibraryFilters {
  author: string | null;
  category: string | null;
  format: string | null;
  search: string;
}

export function asList<T>(value: T[] | null | undefined): T[] {
  return Array.isArray(value) ? value : [];
}

const EMPTY_FILTERS: LibraryFilters = {
  author: null,
  category: null,
  format: null,
  search: "",
};

interface LibraryState {
  books: LibraryItem[];
  continueItems: LibraryItem[];
  recentItems: LibraryItem[];
  authorsList: LibraryAuthor[];
  seriesList: LibrarySeries[];
  facets: LibraryFacets | null;
  status: "idle" | "loading" | "ready" | "error";
  error: string | null;
  filters: LibraryFilters;
  fetchCatalog: () => Promise<void>;
  fetchContinue: () => Promise<void>;
  fetchRecent: () => Promise<void>;
  fetchAuthors: () => Promise<void>;
  fetchSeries: () => Promise<void>;
  fetchItem: (id: string) => Promise<LibraryItem | null>;
  setFilter: (key: keyof LibraryFilters, value: string | null) => void;
  clearFilters: () => void;
  filtered: () => LibraryItem[];
}

export const useLibraryStore = create<LibraryState>()((set, get) => ({
  books: [],
  continueItems: [],
  recentItems: [],
  authorsList: [],
  seriesList: [],
  facets: null,
  status: "idle",
  error: null,
  filters: EMPTY_FILTERS,

  fetchCatalog: async () => {
    if (get().status === "loading") return;
    set({ status: "loading", error: null });
    try {
      const [books, facets] = await Promise.all([
        api<LibraryItem[] | null>("/api/v1/library/books"),
        api<LibraryFacets>("/api/v1/library/facets"),
      ]);
      set({ books: asList(books), facets, status: "ready" });
    } catch (err) {
      set({
        status: "error",
        error: err instanceof Error ? err.message : "failed to load library",
      });
    }
  },

  fetchContinue: async () => {
    try {
      const items = await api<LibraryItem[] | null>("/api/v1/library/continue");
      set({ continueItems: asList(items) });
    } catch (err) {
      console.error("Failed to load continue reading items", err);
    }
  },

  fetchRecent: async () => {
    try {
      const items = await api<LibraryItem[] | null>("/api/v1/library/recent");
      set({ recentItems: asList(items) });
    } catch (err) {
      console.error("Failed to load recent items", err);
    }
  },

  fetchAuthors: async () => {
    try {
      const authors = await api<LibraryAuthor[] | null>("/api/v1/library/authors");
      set({ authorsList: asList(authors) });
    } catch (err) {
      console.error("Failed to load authors", err);
    }
  },

  fetchSeries: async () => {
    try {
      const series = await api<LibrarySeries[] | null>("/api/v1/library/series");
      set({ seriesList: asList(series) });
    } catch (err) {
      console.error("Failed to load series", err);
    }
  },

  fetchItem: async (id: string) => {
    try {
      const item = await api<LibraryItem>(`/api/v1/library/books/${id}`);
      return item;
    } catch (err) {
      console.error("Failed to load item detail", err);
      return null;
    }
  },

  setFilter: (key, value) =>
    set((s) => ({
      filters: {
        ...s.filters,
        [key]: value ?? (key === "search" ? "" : null),
      },
    })),

  clearFilters: () => set({ filters: EMPTY_FILTERS }),

  filtered: () => {
    const { books, filters } = get();
    const q = filters.search.trim().toLowerCase();
    return books.filter((b) => {
      if (filters.author && !asList(b.authors).includes(filters.author))
        return false;
      if (filters.category && !asList(b.categories).includes(filters.category))
        return false;
      if (filters.format && b.format !== filters.format && b.kind !== filters.format) return false;
      if (q) {
        const hay = `${b.title} ${asList(b.authors).join(" ")}`.toLowerCase();
        if (!hay.includes(q)) return false;
      }
      return true;
    });
  },
}));
