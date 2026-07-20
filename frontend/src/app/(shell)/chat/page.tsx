"use client";

import { useEffect, useRef, useState } from "react";
import { Hash, Plus, Send, Smile, X } from "lucide-react";
import { useChatSessionStore } from "@/stores/useChatSessionStore";
import { useAuthStore } from "@/stores/useAuthStore";
import { VaporwaveScene } from "@/components/VaporwaveScene";
import { ChatMessage } from "@/components/chat/ChatMessage";
import { EmojiPicker } from "@/components/chat/EmojiPicker";
import { MentionAutocomplete, type SearchUser } from "@/components/chat/MentionAutocomplete";
import { SharePicker } from "@/components/chat/SharePicker";
import { api } from "@/lib/api";

interface ShareItemMetadata {
  id: string | number;
  title: string;
}

export default function ChatPage() {
  const { channels, activeChannelId, messages, connection, connect, send } =
    useChatSessionStore();
  const [draft, setDraft] = useState("");
  const [showPicker, setShowPicker] = useState(false);
  const [showAttachMenu, setShowAttachMenu] = useState(false);
  const [showSharePicker, setShowSharePicker] = useState(false);
  const [pickerInitialTab, setPickerInitialTab] = useState<"book" | "film">("book");
  const [stagedEmbed, setStagedEmbed] = useState<{ kind: "library_book" | "stream_film"; ref: string; title: string } | null>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  const [mentionQuery, setMentionQuery] = useState<string | null>(null);
  const [matchingUsers, setMatchingUsers] = useState<SearchUser[]>([]);
  const [mentionIndex, setMentionIndex] = useState(0);

  const active = channels.find((c) => c.id === activeChannelId);

  const currentUser = useAuthStore((s) => s.user);
  const canModerate = currentUser?.Roles.some((r) =>
    ["Administrator", "Host", "Curator", "Owner"].includes(r)
  ) ?? false;

  useEffect(() => {
    if (!activeChannelId && channels.length > 0) {
      const first = channels.find((c) => c.type === "text");
      if (first) connect(first.id);
    }
  }, [activeChannelId, channels, connect]);

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const shareKind = params.get("share_kind");
    const shareRef = params.get("share_ref");
    if (shareKind && shareRef) {
      const kind = shareKind as "library_book" | "stream_film";
      const url = kind === "library_book"
        ? `/api/v1/library/books/${shareRef}`
        : `/api/v1/media/items/${shareRef}`;

      api<ShareItemMetadata>(url)
        .then((item) => {
          if (item) {
            setStagedEmbed({
              kind,
              ref: shareRef,
              title: item.title,
            });
          }
        })
        .catch((err) => console.error("failed to fetch shared item", err));

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
      <VaporwaveScene />
      <div className="arenahead">
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
            {stagedEmbed.title} ({stagedEmbed.kind === "library_book" ? "Book" : "Media"})
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
              <div
                style={{
                  position: "absolute",
                  bottom: "100%",
                  left: 0,
                  marginBottom: "8px",
                  background: "var(--surface-2)",
                  border: "1px solid var(--line)",
                  borderRadius: "var(--r-xs)",
                  padding: "4px",
                  display: "flex",
                  flexDirection: "column",
                  gap: "2px",
                  zIndex: 1055,
                  minWidth: "160px",
                  boxShadow: "0 -4px 12px rgba(0,0,0,0.2)",
                  backdropFilter: "blur(8px)",
                }}
              >
                <button
                  onClick={() => {
                    setPickerInitialTab("book");
                    setShowSharePicker(true);
                    setShowAttachMenu(false);
                  }}
                  style={{
                    padding: "6px 12px",
                    background: "transparent",
                    border: "none",
                    borderRadius: "var(--r-xs)",
                    textAlign: "left",
                    color: "var(--ink)",
                    fontSize: "12.5px",
                    cursor: "pointer",
                  }}
                  className="attach-opt-btn"
                >
                  Share from Library
                </button>
                <button
                  onClick={() => {
                    setPickerInitialTab("film");
                    setShowSharePicker(true);
                    setShowAttachMenu(false);
                  }}
                  style={{
                    padding: "6px 12px",
                    background: "transparent",
                    border: "none",
                    borderRadius: "var(--r-xs)",
                    textAlign: "left",
                    color: "var(--ink)",
                    fontSize: "12.5px",
                    cursor: "pointer",
                  }}
                  className="attach-opt-btn"
                >
                  Share from Stream
                </button>
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
    </>
  );
}
