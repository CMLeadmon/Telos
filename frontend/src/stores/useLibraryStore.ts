import { create } from "zustand";
import { api } from "@/lib/api";

export interface LibraryBook {
  id: number;
  title: string;
  authors: string[] | null;
  categories: string[] | null;
  language: string;
  format: string; // "EPUB" | "PDF"
  fileSizeKb: number;
  addedOn: string;
  library: string;
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

// The gateway marshals empty Go slices as JSON null — normalize before use.
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
  books: LibraryBook[];
  facets: LibraryFacets | null;
  status: "idle" | "loading" | "ready" | "error";
  error: string | null;
  filters: LibraryFilters;
  fetchCatalog: () => Promise<void>;
  setFilter: (key: keyof LibraryFilters, value: string | null) => void;
  clearFilters: () => void;
  filtered: () => LibraryBook[];
}

export const useLibraryStore = create<LibraryState>()((set, get) => ({
  books: [],
  facets: null,
  status: "idle",
  error: null,
  filters: EMPTY_FILTERS,

  fetchCatalog: async () => {
    if (get().status === "loading") return;
    set({ status: "loading", error: null });
    try {
      const [books, facets] = await Promise.all([
        api<LibraryBook[] | null>("/api/v1/library/books"),
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
      if (filters.format && b.format !== filters.format) return false;
      if (q) {
        const hay = `${b.title} ${asList(b.authors).join(" ")}`.toLowerCase();
        if (!hay.includes(q)) return false;
      }
      return true;
    });
  },
}));
