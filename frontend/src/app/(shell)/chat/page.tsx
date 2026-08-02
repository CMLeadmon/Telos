"use client";

import { useEffect, useRef, useState } from "react";
import { ChevronLeft, Hash, Plus, Send, Smile, X } from "lucide-react";
import { useChatSessionStore } from "@/stores/useChatSessionStore";
import { useMobileNavStore } from "@/stores/useMobileNavStore";
import { useAuthStore } from "@/stores/useAuthStore";
import { hasCapability } from "@/lib/capabilities";
import { ChatMessage } from "@/components/chat/ChatMessage";
import { EmojiPicker } from "@/components/chat/EmojiPicker";
import { MentionAutocomplete, type SearchUser } from "@/components/chat/MentionAutocomplete";
import { SharePicker, type ShareKind, type ShareTab } from "@/components/chat/SharePicker";
import { ThreadPanel } from "@/components/chat/ThreadPanel";
import { api } from "@/lib/api";

interface ShareItemMetadata {
  id: string | number;
  title: string;
}

// The picker opens on the matching tab, but every tab stays reachable from
// inside it — these are shortcuts, not separate pickers.
const ATTACH_OPTIONS: { tab: ShareTab; label: string }[] = [
  { tab: "book", label: "Share from Library" },
  { tab: "film", label: "Share from Stream" },
  { tab: "file", label: "Share a file" },
];

// Files carry no metadata endpoint, but the browser's IDs are
// base64url(relative path), so the label comes out of the ref itself — nothing
// separate to trust. The gateway still builds the authoritative snapshot when
// the message is sent; this is only the staged chip's caption.
function fileLabelFromRef(ref: string): string {
  const fallback = "Shared file";
  try {
    const b64 = ref.replace(/-/g, "+").replace(/_/g, "/");
    const binary = atob(b64.padEnd(Math.ceil(b64.length / 4) * 4, "="));
    const decoded = new TextDecoder("utf-8", { fatal: true }).decode(
      Uint8Array.from(binary, (c) => c.charCodeAt(0)),
    );
    // A UUID decodes to bytes too; only a plausible path is worth showing.
    if (!decoded || [...decoded].some((c) => c.charCodeAt(0) < 0x20)) return fallback;
    return decoded.split("/").pop() || fallback;
  } catch {
    return fallback;
  }
}

export default function ChatPage() {
  const { channels, activeChannelId, messages, connection, connect, send } =
    useChatSessionStore();
  const openChannelDrawer = useMobileNavStore((s) => s.openChannelDrawer);
  const [draft, setDraft] = useState("");
  const [showPicker, setShowPicker] = useState(false);
  const [showAttachMenu, setShowAttachMenu] = useState(false);
  const [showSharePicker, setShowSharePicker] = useState(false);
  const [pickerInitialTab, setPickerInitialTab] = useState<ShareTab>("book");
  const [stagedEmbed, setStagedEmbed] = useState<{ kind: ShareKind; ref: string; title: string } | null>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  const [mentionQuery, setMentionQuery] = useState<string | null>(null);
  const [matchingUsers, setMatchingUsers] = useState<SearchUser[]>([]);
  const [mentionIndex, setMentionIndex] = useState(0);

  const active = channels.find((c) => c.id === activeChannelId);

  const currentUser = useAuthStore((s) => s.user);
  const canModerate =
    hasCapability(currentUser, "moderate_chat") ||
    hasCapability(currentUser, "manage_messages");

  useEffect(() => {
    if (!activeChannelId && channels.length > 0) {
      connect(channels[0].id);
    }
  }, [activeChannelId, channels, connect]);

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const shareKind = params.get("share_kind");
    const shareRef = params.get("share_ref");
    if (shareKind === "library_book" || shareKind === "stream_film" || shareKind === "file") {
      if (!shareRef) return;

      if (shareKind === "file") {
        // The label is derived from the ref rather than fetched, but it still
        // has to land out of a callback — this effect may not setState inline.
        void Promise.resolve(fileLabelFromRef(shareRef)).then((title) => {
          setStagedEmbed({ kind: "file", ref: shareRef, title });
        });
      } else {
        const url =
          shareKind === "library_book"
            ? `/api/v1/library/books/${shareRef}`
            : `/api/v1/media/items/${shareRef}`;

        api<ShareItemMetadata>(url)
          .then((item) => {
            if (item) {
              setStagedEmbed({
                kind: shareKind,
                ref: shareRef,
                title: item.title,
              });
            }
          })
          .catch((err) => console.error("failed to fetch shared item", err));
      }

      const newUrl = window.location.pathname;
      window.history.replaceState({}, "", newUrl);
    }
  }, []);

  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight });
  }, [messages]);

  useEffect(() => {
    if (mentionQuery === null) return;
    const controller = new AbortController();
    const delay = setTimeout(() => {
      api<SearchUser[]>(`/api/v1/users/search?q=${encodeURIComponent(mentionQuery)}&limit=8`, {
        signal: controller.signal,
      })
        .then((res) => {
          setMatchingUsers(res || []);
          setMentionIndex(0);
        })
        .catch(() => {});
    }, 150);

    return () => {
      clearTimeout(delay);
      controller.abort();
    };
  }, [mentionQuery]);

  const checkMentions = (val: string, selectionStart: number | null) => {
    if (selectionStart === null) {
      setMentionQuery(null);
      setMatchingUsers([]);
      setMentionIndex(0);
      return;
    }
    const beforeCursor = val.slice(0, selectionStart);
    const match = beforeCursor.match(/(?:^|\s)@(\w*)$/);
    if (match) {
      setMentionQuery(match[1]);
    } else {
      setMentionQuery(null);
      setMatchingUsers([]);
      setMentionIndex(0);
    }
  };

  const handleChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const val = e.target.value;
    setDraft(val);
    checkMentions(val, e.target.selectionStart);
  };

  const handleKeyUp = (e: React.KeyboardEvent<HTMLInputElement>) => {
    checkMentions(draft, e.currentTarget.selectionStart);
  };

  const handleClick = (e: React.MouseEvent<HTMLInputElement>) => {
    checkMentions(draft, e.currentTarget.selectionStart);
  };

  const selectMention = (user: SearchUser) => {
    if (!inputRef.current) return;
    const selectionStart = inputRef.current.selectionStart || 0;
    const beforeCursor = draft.slice(0, selectionStart);
    const afterCursor = draft.slice(selectionStart);

    const replacedBefore = beforeCursor.replace(/(?:^|\s)@\w*$/, (match) => {
      const isSpacePrefix = match.startsWith(" ");
      return (isSpacePrefix ? " " : "") + `@${user.username} `;
    });

    setDraft(replacedBefore + afterCursor);
    setMentionQuery(null);
    setMatchingUsers([]);

    setTimeout(() => {
      if (inputRef.current) {
        inputRef.current.focus();
        const newCursorPos = replacedBefore.length;
        inputRef.current.setSelectionRange(newCursorPos, newCursorPos);
      }
    }, 0);
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (matchingUsers.length > 0) {
      if (e.key === "ArrowDown") {
        e.preventDefault();
        setMentionIndex((prev) => (prev + 1) % matchingUsers.length);
        return;
      }
      if (e.key === "ArrowUp") {
        e.preventDefault();
        setMentionIndex((prev) => (prev - 1 + matchingUsers.length) % matchingUsers.length);
        return;
      }
      if (e.key === "Enter") {
        e.preventDefault();
        selectMention(matchingUsers[mentionIndex]);
        return;
      }
      if (e.key === "Escape") {
        e.preventDefault();
        setMentionQuery(null);
        setMatchingUsers([]);
        return;
      }
    }

    if (e.key === "Enter") {
      submit();
    }
  };

  const submit = () => {
    if (draft.trim() || stagedEmbed) {
      send(draft, stagedEmbed ? { kind: stagedEmbed.kind, ref: stagedEmbed.ref } : undefined);
      setDraft("");
      setStagedEmbed(null);
      setMentionQuery(null);
      setMatchingUsers([]);
    }
  };

  return (
    <>
      <div className="arenahead">
        {/* Mobile has no rail, so the channel list is reached by backing out of
            the current channel — the DS mobile chat pattern. Hidden on desktop,
            where the rail already lists every channel. */}
        <button
          className="iconbtn chan-back"
          aria-label="Switch channel"
          data-testid="mobile-channel-switch"
          onClick={openChannelDrawer}
        >
          <ChevronLeft size={22} />
        </button>
        <div className="name">
          <Hash size={17} />
          {active?.name ?? "…"}
        </div>
        <span className="kicker">
          {connection === "open" ? "// live" : `// ${connection}`}
        </span>
      </div>
      <div className="banner">be on the net, but not of the net</div>

      <div className="msgs" ref={scrollRef}>
        {messages.length === 0 && (
          <div className="placeholder">
            <h2>Quiet in here.</h2>
            <p>say something — the node keeps the last 50 messages warm.</p>
          </div>
        )}
        {messages.map((m) => (
          <ChatMessage key={m.id} message={m} canModerate={canModerate} />
        ))}
      </div>

      {stagedEmbed && (
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: "8px",
            background: "var(--surface-2)",
            border: "1px solid var(--line)",
            borderBottom: "none",
            borderRadius: "var(--r) var(--r) 0 0",
            padding: "6px 12px",
            fontSize: "12px",
            color: "var(--ink)",
            maxWidth: "600px",
            margin: "0 auto",
            position: "relative",
            zIndex: 5,
          }}
        >
          <span style={{ fontWeight: 600, color: "var(--accent)" }}>
            Staged Embed:
          </span>
          <span style={{ flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            {stagedEmbed.title} (
            {stagedEmbed.kind === "library_book"
              ? "Book"
              : stagedEmbed.kind === "stream_film"
                ? "Media"
                : "File"}
            )
          </span>
          <button
            onClick={() => setStagedEmbed(null)}
            style={{
              background: "transparent",
              border: "none",
              cursor: "pointer",
              color: "var(--faint)",
              display: "flex",
              alignItems: "center",
            }}
          >
            <X size={14} />
          </button>
        </div>
      )}

      <div className="composer">
        <div style={{ position: "relative" }}>
          <button className="iconbtn" aria-label="attach" onClick={() => setShowAttachMenu(!showAttachMenu)}>
            <Plus size={20} />
          </button>
          {showAttachMenu && (
            <>
              <div style={{ position: "fixed", inset: 0, zIndex: 1050 }} onClick={() => setShowAttachMenu(false)} />
              <div className="attach-menu">
                {ATTACH_OPTIONS.map((opt) => (
                  <button
                    key={opt.tab}
                    className="attach-opt-btn"
                    onClick={() => {
                      setPickerInitialTab(opt.tab);
                      setShowSharePicker(true);
                      setShowAttachMenu(false);
                    }}
                  >
                    {opt.label}
                  </button>
                ))}
              </div>
            </>
          )}
        </div>
        <div className="cwrap" style={{ position: "relative" }}>
          <input
            ref={inputRef}
            placeholder={active ? `Message #${active.name}…` : "Message…"}
            value={draft}
            onChange={handleChange}
            onKeyDown={handleKeyDown}
            onKeyUp={handleKeyUp}
            onClick={handleClick}
          />
          {matchingUsers.length > 0 && (
            <MentionAutocomplete
              users={matchingUsers}
              selectedIndex={mentionIndex}
              onSelect={selectMention}
            />
          )}
        </div>
        <div style={{ position: "relative" }}>
          <button className="iconbtn" aria-label="emoji" onClick={() => setShowPicker(!showPicker)}>
            <Smile size={18} />
          </button>
          {showPicker && (
            <EmojiPicker
              onPick={(emoji) => {
                setDraft((prev) => prev + emoji);
              }}
              onClose={() => setShowPicker(false)}
            />
          )}
        </div>
        <button className="send" aria-label="send" onClick={submit}>
          <Send size={17} />
        </button>
      </div>

      {showSharePicker && (
        <SharePicker
          defaultTab={pickerInitialTab}
          onClose={() => setShowSharePicker(false)}
          onPick={(kind, ref, title) => {
            setStagedEmbed({ kind, ref, title });
          }}
        />
      )}
      <ThreadPanel />
    </>
  );
}
