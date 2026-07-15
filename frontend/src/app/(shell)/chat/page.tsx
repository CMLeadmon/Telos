"use client";

import { useEffect, useRef, useState } from "react";
import { Hash, Plus, Send } from "lucide-react";
import { useChatSessionStore } from "@/stores/useChatSessionStore";
import { useAuthStore } from "@/stores/useAuthStore";
import { VaporwaveScene } from "@/components/VaporwaveScene";
import { ChatMessage } from "@/components/chat/ChatMessage";

export default function ChatPage() {
  const { channels, activeChannelId, messages, connection, connect, send } =
    useChatSessionStore();
  const [draft, setDraft] = useState("");
  const scrollRef = useRef<HTMLDivElement>(null);

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

  const submit = () => {
    if (draft.trim()) {
      send(draft);
      setDraft("");
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
        <div className="cwrap">
          <input
            placeholder={active ? `Message #${active.name}…` : "Message…"}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && submit()}
          />
        </div>
        <button className="send" aria-label="send" onClick={submit}>
          <Send size={17} />
        </button>
      </div>
    </>
  );
}
