"use client";

import { useEffect, useRef, useState } from "react";
import { Hash, Plus, Send } from "lucide-react";
import { useChatSessionStore } from "@/stores/useChatSessionStore";
import { VaporwaveScene } from "@/components/VaporwaveScene";

const AVATAR_HUES = ["--rose", "--cyan", "--violet", "--indigo", "--azure"];

function avatarHue(name: string): string {
  let h = 0;
  for (const ch of name) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  return `var(${AVATAR_HUES[h % AVATAR_HUES.length]})`;
}

export default function ChatPage() {
  const { channels, activeChannelId, messages, connection, connect, send } =
    useChatSessionStore();
  const [draft, setDraft] = useState("");
  const scrollRef = useRef<HTMLDivElement>(null);

  const active = channels.find((c) => c.id === activeChannelId);

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
          <div className="msg" key={m.id}>
            <div className="av" style={{ background: avatarHue(m.sender) }}>
              {m.avatar}
            </div>
            <div style={{ minWidth: 0 }}>
              <div className="mhead">
                <span className="mname">{m.sender}</span>
                <span className="rolepill">{m.role}</span>
                <span className="mtime">{m.timestamp}</span>
              </div>
              <p className="mbody">{m.content}</p>
            </div>
          </div>
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
