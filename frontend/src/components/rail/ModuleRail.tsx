"use client";

import { usePathname } from "next/navigation";
import {
  Clapperboard,
  Folder,
  HardDrive,
  Hash,
  Library as LibraryIcon,
  Mic,
  MonitorPlay,
  Music,
  Server,
  User,
} from "lucide-react";
import { useChatSessionStore } from "@/stores/useChatSessionStore";
import { useMediaStore, type MediaLibrary } from "@/stores/useMediaStore";
import { asList, useLibraryStore } from "@/stores/useLibraryStore";
import { useFilesStore } from "@/stores/useFilesStore";
import { useAuthStore } from "@/stores/useAuthStore";
import { useSettingsStore } from "@/stores/useSettingsStore";
import { SETTINGS_SECTIONS } from "@/lib/settingsSections";
import { hasCapability } from "@/lib/capabilities";

// The rail below the module nav belongs to whichever module is open — Chat
// lists text channels, Stream lists Jellyfin libraries, Library lists shelves
// and collections. Every group reads already-loaded store data; nothing here
// triggers a fetch, so the rail can never be the reason a request is made.

function libraryIcon(lib: MediaLibrary) {
  switch (lib.collectionType) {
    case "movies":
      return Clapperboard;
    case "tvshows":
      return MonitorPlay;
    case "music":
      return Music;
    case "podcasts":
      return Mic;
    default:
      return lib.type === "audio" ? Music : MonitorPlay;
  }
}

function ChatGroups() {
  const { channels, activeChannelId, connect } = useChatSessionStore();
  if (channels.length === 0) return null;
  return (
    <div>
      <h3 className="grouplabel">Text channels</h3>
      <div className="chanlist">
        {channels.map((c) => (
          <button
            key={c.id}
            className={`chan${c.id === activeChannelId ? " on" : ""}`}
            onClick={() => connect(c.id)}
          >
            <span className="l">
              <Hash size={15} />
              {c.name}
            </span>
          </button>
        ))}
      </div>
    </div>
  );
}

function StreamGroups() {
  const libraries = useMediaStore((s) => s.libraries);
  const activeLibraryId = useMediaStore((s) => s.activeLibraryId);
  const selectLibrary = useMediaStore((s) => s.selectLibrary);
  if (libraries.length === 0) return null;
  return (
    <div>
      <h3 className="grouplabel">Browse</h3>
      <div className="chanlist">
        {libraries.map((lib) => {
          const Icon = libraryIcon(lib);
          return (
            <button
              key={lib.id}
              className={`chan${lib.id === activeLibraryId ? " on" : ""}`}
              onClick={() => {
                selectLibrary(lib.id);
                document
                  .getElementById(`lib-${lib.id}`)
                  ?.scrollIntoView({ behavior: "smooth", block: "start" });
              }}
            >
              <span className="l">
                <Icon size={15} />
                {lib.name}
              </span>
            </button>
          );
        })}
      </div>
    </div>
  );
}

function LibraryGroups() {
  const facets = useLibraryStore((s) => s.facets);
  const filters = useLibraryStore((s) => s.filters);
  const setFilter = useLibraryStore((s) => s.setFilter);
  const clearFilters = useLibraryStore((s) => s.clearFilters);
  const books = useLibraryStore((s) => s.books);

  const categories = asList(facets?.categories);
  const authors = asList(facets?.authors);
  const narrowed =
    filters.author !== null || filters.category !== null || filters.format !== null;

  // Toggling an active facet clears it, so a second click on the same row is
  // the way back to the whole shelf.
  const toggle = (key: "author" | "category", value: string) =>
    setFilter(key, filters[key] === value ? null : value);

  return (
    <>
      <div>
        <h3 className="grouplabel">Shelves</h3>
        <div className="chanlist">
          <button
            className={`chan${narrowed ? "" : " on"}`}
            onClick={clearFilters}
          >
            <span className="l">
              <LibraryIcon size={15} />
              All books
            </span>
            <span className="joinlbl">{books.length}</span>
          </button>
        </div>
      </div>

      {categories.length > 0 && (
        <div>
          <h3 className="grouplabel">Collections</h3>
          <div className="chanlist">
            {categories.slice(0, 12).map((c) => (
              <button
                key={c.value}
                className={`chan${filters.category === c.value ? " on" : ""}`}
                onClick={() => toggle("category", c.value)}
              >
                <span className="l">
                  <Hash size={15} />
                  {c.value}
                </span>
                <span className="joinlbl">{c.count}</span>
              </button>
            ))}
          </div>
        </div>
      )}

      {authors.length > 0 && (
        <div>
          <h3 className="grouplabel">Authors</h3>
          <div className="chanlist">
            {authors.slice(0, 12).map((a) => (
              <button
                key={a.value}
                className={`chan${filters.author === a.value ? " on" : ""}`}
                onClick={() => toggle("author", a.value)}
              >
                <span className="l">
                  <User size={15} />
                  {a.value}
                </span>
                <span className="joinlbl">{a.count}</span>
              </button>
            ))}
          </div>
        </div>
      )}
    </>
  );
}

function FilesGroups() {
  const rootFolders = useFilesStore((s) => s.rootFolders);
  const path = useFilesStore((s) => s.path);
  const navigateTo = useFilesStore((s) => s.navigateTo);
  if (rootFolders.length === 0) return null;

  // The current location is whichever root the open path descends from.
  const activeRoot = path.split("/").filter(Boolean)[0] ?? "";

  return (
    <div>
      <h3 className="grouplabel">Locations</h3>
      <div className="chanlist">
        <button
          className={`chan${activeRoot === "" ? " on" : ""}`}
          onClick={() => void navigateTo("")}
        >
          <span className="l">
            <HardDrive size={15} />
            all files
          </span>
        </button>
        {rootFolders.map((f) => (
          <button
            key={f.id}
            className={`chan${activeRoot === f.name ? " on" : ""}`}
            onClick={() => void navigateTo(f.path)}
          >
            <span className="l">
              <Folder size={15} />
              {f.name}
            </span>
          </button>
        ))}
      </div>
    </div>
  );
}

// Settings has no mockup, so this follows the pattern the four modules
// establish: sub-navigation belongs in the rail, not a second in-arena
// sidebar. Split into the same User settings / Administration groups.
function SettingsGroups() {
  const user = useAuthStore((s) => s.user);
  const activeSection = useSettingsStore((s) => s.activeSection);
  const setSection = useSettingsStore((s) => s.setSection);

  // Client-side visibility only; every admin endpoint enforces server-side.
  const visible = SETTINGS_SECTIONS.filter(
    (s) => !s.admin || (s.capability && hasCapability(user, s.capability)),
  );
  const groups: Array<[string, typeof visible]> = [
    ["User settings", visible.filter((s) => !s.admin)],
    ["Administration", visible.filter((s) => s.admin)],
  ];

  return (
    <>
      {groups.map(([label, sections]) =>
        sections.length === 0 ? null : (
          <div key={label}>
            <h3 className="grouplabel">{label}</h3>
            <div className="chanlist">
              {sections.map(({ id, label: name, icon: Icon }) => (
                <button
                  key={id}
                  className={`chan${activeSection === id ? " on" : ""}`}
                  onClick={() => setSection(id)}
                >
                  <span className="l">
                    <Icon size={15} />
                    {name}
                  </span>
                </button>
              ))}
            </div>
          </div>
        ),
      )}
    </>
  );
}

export function ModuleRail() {
  const pathname = usePathname();
  if (pathname.startsWith("/chat")) return <ChatGroups />;
  if (pathname.startsWith("/stream")) return <StreamGroups />;
  if (pathname.startsWith("/library")) return <LibraryGroups />;
  if (pathname.startsWith("/files")) return <FilesGroups />;
  if (pathname.startsWith("/settings")) return <SettingsGroups />;
  return null;
}

// The footer reports whatever the open module actually depends on.
export function ModuleRailFooter() {
  const pathname = usePathname();
  const books = useLibraryStore((s) => s.books);

  if (pathname.startsWith("/stream")) {
    return (
      <div className="railfoot">
        <div className="row">
          <Server size={11} style={{ color: "var(--cyan)" }} /> jellyfin · synced
        </div>
        <div className="row">node: telos-node-1</div>
      </div>
    );
  }

  if (pathname.startsWith("/library") && books.length > 0) {
    // The normalized Library contract no longer carries per-item byte size, so
    // the footer reports the count rather than inventing a total.
    return (
      <div className="railfoot">
        <div className="row">
          <LibraryIcon size={11} /> {books.length} item
          {books.length === 1 ? "" : "s"}
        </div>
        <div className="row">node: telos-node-1</div>
      </div>
    );
  }

  return (
    <div className="railfoot">
      <div className="row">
        <span className="dot" style={{ width: 5, height: 5 }} /> tunnel: encrypted
      </div>
      <div className="row">node: telos-node-1</div>
    </div>
  );
}
