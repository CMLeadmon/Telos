"use client";

import { useEffect, useState, useRef } from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import {
  BookOpen,
  Folder,
  Hash,
  LogOut,
  MessageSquare,
  Moon,
  Search,
  Settings,
  Sun,
  Tv,
} from "lucide-react";
import { useAuthStore } from "@/stores/useAuthStore";
import { useChatSessionStore, type Channel } from "@/stores/useChatSessionStore";
import { useThemeStore } from "@/stores/useThemeStore";
import { usePreferencesStore } from "@/stores/usePreferencesStore";
import { api, avatarUrl } from "@/lib/api";
import { openNodeResource } from "@/lib/platform";
import { hasCapability } from "@/lib/capabilities";
import { BrandLogo } from "@/components/BrandLogo";
import { ChatAside } from "@/components/chat/ChatAside";
import { MobileNavigation } from "@/components/MobileNavigation";
import { ModuleRail, ModuleRailFooter } from "@/components/rail/ModuleRail";
import { ConnectivityBanner } from "@/components/ConnectivityBanner";

const MODULES = [
  { href: "/chat", label: "Chat", icon: MessageSquare, capability: "view_channel" },
  { href: "/stream", label: "Stream", icon: Tv, capability: "view_media" },
  { href: "/library", label: "Library", icon: BookOpen, capability: "view_library" },
  { href: "/files", label: "Files", icon: Folder, capability: "view_files" },
];


export function AppShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const router = useRouter();
  const { user, status, fetchMe, logout } = useAuthStore();
  const theme = useThemeStore((s) => s.theme);
  // Theme toggling routes through usePreferencesStore so it persists server-side.
  const prefsLoaded = usePreferencesStore((s) => s.loaded);
  const loadPrefs = usePreferencesStore((s) => s.load);
  const themeSaveFailed = usePreferencesStore((s) => s.saveStatus === "error");
  const prefsSaveError = usePreferencesStore((s) => s.saveError);
  const { channels, fetchChannels, connect, onlineCount } =
    useChatSessionStore();

  // Bind the app height to the visual viewport so the bottom nav and the chat
  // composer stay above the on-screen keyboard on mobile (100dvh does not shrink
  // when the keyboard opens). Writes a CSS var, not React state, so it never
  // triggers a re-render. A no-op where visualViewport is unavailable.
  useEffect(() => {
    const vv = window.visualViewport;
    if (!vv) return;
    const sync = () => {
      document.documentElement.style.setProperty("--app-h", `${vv.height}px`);
    };
    sync();
    vv.addEventListener("resize", sync);
    vv.addEventListener("scroll", sync);
    return () => {
      vv.removeEventListener("resize", sync);
      vv.removeEventListener("scroll", sync);
      document.documentElement.style.removeProperty("--app-h");
    };
  }, []);

  interface SearchUser {
    id: string;
    username: string;
    displayName: string;
    avatarUrl?: string;
  }

  interface SearchBook {
    id: string;
    title: string;
    authors: string[] | null;
    categories: string[] | null;
    format: string;
  }

  interface SearchMedia {
    id: string;
    title: string;
    duration: string;
    type: string;
    isFolder: boolean;
  }

  interface SearchFileItem {
    id: string;
    filename: string;
    sizeBytes: number;
    mimeType: string;
    createdAt: string;
  }

  interface SearchResults {
    channels: Channel[];
    users: SearchUser[];
    books: SearchBook[];
    media: SearchMedia[];
    files: SearchFileItem[];
  }

  type FlatSearchItem =
    | (Channel & { $type: "channel" })
    | (SearchUser & { $type: "user" })
    | (SearchBook & { $type: "book" })
    | (SearchMedia & { $type: "media" })
    | (SearchFileItem & { $type: "file" });

  // Global search state
  const [searchQuery, setSearchQuery] = useState("");
  const [searchResults, setSearchResults] = useState<SearchResults | null>(null);
  const [searchLoading, setSearchLoading] = useState(false);
  const [searchFocused, setSearchFocused] = useState(false);
  const [searchActiveIndex, setSearchActiveIndex] = useState(0);
  const searchContainerRef = useRef<HTMLDivElement>(null);

  // Debounced search fetch
  useEffect(() => {
    if (searchQuery.trim().length < 2) {
      return;
    }

    const controller = new AbortController();
    const delay = setTimeout(() => {
      api<SearchResults>(`/api/v1/search?q=${encodeURIComponent(searchQuery)}`, {
        signal: controller.signal,
      })
        .then((res) => {
          setSearchResults(res);
          setSearchActiveIndex(0);
        })
        .catch((err) => {
          if (err instanceof Error && err.name !== "AbortError") {
            console.error("search failed", err);
          }
        })
        .finally(() => {
          setSearchLoading(false);
        });
    }, 250);

    return () => {
      clearTimeout(delay);
      controller.abort();
    };
  }, [searchQuery]);

  // Click outside to dismiss search results
  useEffect(() => {
    const handleOutsideClick = (e: MouseEvent) => {
      if (
        searchContainerRef.current &&
        !searchContainerRef.current.contains(e.target as Node)
      ) {
        setSearchFocused(false);
      }
    };
    document.addEventListener("mousedown", handleOutsideClick);
    return () => {
      document.removeEventListener("mousedown", handleOutsideClick);
    };
  }, []);

  const flatResults: FlatSearchItem[] = searchResults
    ? [
        ...(searchResults.channels || []).map((c) => ({ ...c, $type: "channel" as const })),
        ...(searchResults.users || []).map((u) => ({ ...u, $type: "user" as const })),
        ...(searchResults.books || []).map((b) => ({ ...b, $type: "book" as const })),
        ...(searchResults.media || []).map((m) => ({ ...m, $type: "media" as const })),
        ...(searchResults.files || []).map((f) => ({ ...f, $type: "file" as const })),
      ]
    : [];

  const handleSelectItem = (item: FlatSearchItem) => {
    setSearchFocused(false);
    setSearchQuery("");

    if (item.$type === "channel") {
      if (!pathname.startsWith("/chat")) {
        router.push("/chat/");
      }
      connect(item.id);
    } else if (item.$type === "user") {
      router.push("/chat/");
    } else if (item.$type === "book") {
      router.push(`/library?read=${encodeURIComponent(item.id)}`);
    } else if (item.$type === "media") {
      router.push(`/stream?play=${encodeURIComponent(item.id)}`);
    } else if (item.$type === "file") {
      // Was a bare relative path opened in a new tab, which is wrong twice over
      // in the native client: it resolves against the app bundle instead of the
      // node, and a navigation carries no bearer token even when it does not.
      void openNodeResource(
        `/api/v1/files/${encodeURIComponent(item.id)}/download`,
        item.filename,
      );
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (!searchFocused || flatResults.length === 0) return;

    if (e.key === "ArrowDown") {
      e.preventDefault();
      setSearchActiveIndex((prev) => (prev + 1) % flatResults.length);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setSearchActiveIndex((prev) =>
        prev === 0 ? flatResults.length - 1 : prev - 1
      );
    } else if (e.key === "Enter") {
      e.preventDefault();
      const item = flatResults[searchActiveIndex];
      if (item) {
        handleSelectItem(item);
      }
    } else if (e.key === "Escape") {
      setSearchFocused(false);
      e.currentTarget.blur();
    }
  };

  const renderSearchSection = <T extends { id: string }>(
    title: string,
    items: T[] | undefined,
    type: "channel" | "user" | "book" | "media" | "file"
  ) => {
    if (!items || items.length === 0) return null;
    return (
      <div className="search-section">
        <div className="search-section-title">{title}</div>
        {items.map((item) => {
          const flatIndex = flatResults.findIndex(
            (f) => f.id === item.id && f.$type === type
          );
          const isActive = flatIndex === searchActiveIndex;

          return (
            <div
              key={`${type}-${item.id}`}
              className={`search-item${isActive ? " active" : ""}`}
              onClick={() => handleSelectItem(flatResults[flatIndex])}
              onMouseEnter={() => setSearchActiveIndex(flatIndex)}
            >
              {type === "channel" && (
                <>
                  <Hash size={14} className="search-icon" />
                  <span className="search-title">{(item as unknown as Channel).name}</span>
                  <span className="search-meta">channel</span>
                </>
              )}
              {type === "user" && (
                <>
                  <span className="search-avatar-wrapper">
                    {(item as unknown as SearchUser).avatarUrl ? (
                      // eslint-disable-next-line @next/next/no-img-element
                      <img src={(item as unknown as SearchUser).avatarUrl} alt="" className="search-avatar" />
                    ) : (
                      <span className="search-avatar-placeholder" />
                    )}
                  </span>
                  <span className="search-title">{(item as unknown as SearchUser).displayName || (item as unknown as SearchUser).username}</span>
                  <span className="search-meta">@{(item as unknown as SearchUser).username}</span>
                </>
              )}
              {type === "book" && (
                <>
                  <BookOpen size={14} className="search-icon" />
                  <div className="search-text-group">
                    <span className="search-title">{(item as unknown as SearchBook).title}</span>
                    <span className="search-meta">
                      {(item as unknown as SearchBook).authors?.join(", ") ||
                        "Unknown Author"}
                    </span>
                  </div>
                  <span className="search-badge">{(item as unknown as SearchBook).format}</span>
                </>
              )}
              {type === "media" && (
                <>
                  <Tv size={14} className="search-icon" />
                  <div className="search-text-group">
                    <span className="search-title">{(item as unknown as SearchMedia).title}</span>
                    <span className="search-meta">{(item as unknown as SearchMedia).type}</span>
                  </div>
                  {(item as unknown as SearchMedia).duration && <span className="search-badge">{(item as unknown as SearchMedia).duration}</span>}
                </>
              )}
              {type === "file" && (
                <>
                  <Folder size={14} className="search-icon" />
                  <div className="search-text-group">
                    <span className="search-title">{(item as unknown as SearchFileItem).filename}</span>
                    <span className="search-meta">
                      {(((item as unknown as SearchFileItem).sizeBytes || 0) / 1024).toFixed(1)} KB · {(item as unknown as SearchFileItem).mimeType}
                    </span>
                  </div>
                </>
              )}
            </div>
          );
        })}
      </div>
    );
  };

  useEffect(() => {
    if (status === "unknown") void fetchMe();
  }, [status, fetchMe]);

  useEffect(() => {
    if (status === "authenticated" && !prefsLoaded) void loadPrefs();
  }, [status, prefsLoaded, loadPrefs]);

  useEffect(() => {
    if (status === "anonymous") router.replace("/login/");
  }, [status, router]);

  useEffect(() => {
    if (status === "authenticated" && channels.length === 0) {
      fetchChannels().catch(() => {});
    }
  }, [status, channels.length, fetchChannels]);

  if (status !== "authenticated") {
    return (
      <div className="authwrap">
        <span className="kicker">{"// connecting to node…"}</span>
      </div>
    );
  }

  const onChat = pathname.startsWith("/chat");

  return (
    <div className="app app--viewport" data-testid="app-shell">
      <ConnectivityBanner />
      <div className="topbar">
        <div className="brand">
          <BrandLogo size={62} />
          {/* Per-theme, not an inconsistency: the ink mockups set "Telos",
              the synthwave ones "TELOS". */}
          <span className="word">{theme === "ink" ? "Telos" : "TELOS"}</span>
        </div>
        <div className="searchbar" ref={searchContainerRef}>
          <Search size={16} style={{ position: "absolute", left: 14 }} />
          <input
            placeholder="Search channels, users, books, media, files…"
            value={searchQuery}
            onChange={(e) => {
              const val = e.target.value;
              setSearchQuery(val);
              setSearchFocused(true);
              if (val.trim().length < 2) {
                setSearchResults(null);
                setSearchLoading(false);
              } else {
                setSearchLoading(true);
              }
            }}
            onFocus={() => setSearchFocused(true)}
            onKeyDown={handleKeyDown}
          />
          {searchLoading && (
            <span className="search-loader">
              <span className="search-spinner" />
            </span>
          )}
          {searchFocused && searchQuery.trim().length >= 2 && (
            <div className="searchbar-results">
              {flatResults.length === 0 ? (
                <div className="search-empty">
                  {searchLoading ? "Searching..." : "No results found"}
                </div>
              ) : (
                <>
                  {renderSearchSection("Channels", searchResults?.channels, "channel")}
                  {renderSearchSection("Users", searchResults?.users, "user")}
                  {renderSearchSection("Books", searchResults?.books, "book")}
                  {renderSearchSection("Media", searchResults?.media, "media")}
                  {renderSearchSection("Files", searchResults?.files, "file")}
                </>
              )}
            </div>
          )}
        </div>
        <div className="topacts">
          <span className="chip">
            <span className="dot" style={{ background: "var(--cyan)" }} />
            {onlineCount} online
          </span>
          <span className="chip">
            {user?.HasAvatar ? (
              // eslint-disable-next-line @next/next/no-img-element
              <img className="chipav" src={avatarUrl(user.ID)} alt="" />
            ) : (
              <span className="dot" />
            )}{" "}
            {user?.DisplayName || user?.Username}
          </span>
          <Link
            href="/settings"
            className={`iconbtn${pathname.startsWith("/settings") ? " on" : ""}`}
            aria-label="settings"
          >
            <Settings size={18} />
          </Link>
          {/* A failed save used to be invisible here: the icon flipped, the
              PUT 400'd, and the theme silently reverted on the next load.
              Surface it so the failure is visible where the action happens. */}
          <button
            className={`iconbtn${themeSaveFailed ? " save-failed" : ""}`}
            aria-label="toggle theme"
            title={
              themeSaveFailed
                ? `Theme not saved: ${prefsSaveError ?? "unknown error"}`
                : undefined
            }
            onClick={() =>
              void usePreferencesStore
                .getState()
                .save({ theme: theme === "synthwave" ? "ink" : "synthwave" })
            }
          >
            {theme === "synthwave" ? <Sun size={18} /> : <Moon size={18} />}
          </button>
          {themeSaveFailed && (
            <span role="status" className="sr-only">
              {`Theme not saved: ${prefsSaveError ?? "unknown error"}`}
            </span>
          )}
          <button
            className="iconbtn"
            aria-label="log out"
            onClick={() => void logout()}
          >
            <LogOut size={18} />
          </button>
        </div>
      </div>

      <div className="shellbody">
        <aside className="rail">
          <div className="scroll">
            <nav className="modnav">
              {MODULES.filter((m) => hasCapability(user, m.capability)).map(
                ({ href, label, icon: Icon }) => (
                  <Link
                    key={href}
                    href={href}
                    className={`navbtn${pathname.startsWith(href) ? " on" : ""}`}
                  >
                    <Icon size={18} />
                    {label}
                  </Link>
                ),
              )}
            </nav>

            <ModuleRail />
          </div>
          <ModuleRailFooter />
        </aside>

        <main className="arena">{children}</main>
        {onChat && <ChatAside />}
      </div>

      <MobileNavigation />
    </div>
  );
}
