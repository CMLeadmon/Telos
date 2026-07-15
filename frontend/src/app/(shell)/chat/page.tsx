"use client";

import { useEffect, useRef, useState } from "react";
import { Hash, Plus, Send, Smile } from "lucide-react";
import { useChatSessionStore } from "@/stores/useChatSessionStore";
import { useAuthStore } from "@/stores/useAuthStore";
import { VaporwaveScene } from "@/components/VaporwaveScene";
import { ChatMessage } from "@/components/chat/ChatMessage";
import { EmojiPicker } from "@/components/chat/EmojiPicker";
import { MentionAutocomplete, type SearchUser } from "@/components/chat/MentionAutocomplete";
import { api } from "@/lib/api";

export default function ChatPage() {
  const { channels, activeChannelId, messages, connection, connect, send } =
    useChatSessionStore();
  const [draft, setDraft] = useState("");
  const [showPicker, setShowPicker] = useState(false);
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
    if (draft.trim()) {
      send(draft);
      setDraft("");
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

      <div className="composer">
        <button className="iconbtn" aria-label="attach">
          <Plus size={20} />
        </button>
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
    </>
  );
}
