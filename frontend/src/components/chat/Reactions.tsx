"use client";

import React from "react";
import { useChatSessionStore } from "@/stores/useChatSessionStore";
import { useAuthStore } from "@/stores/useAuthStore";

interface ReactionsProps {
  messageId: string;
  reactions: {
    emoji: string;
    count: number;
    users: string[];
  }[];
}

export function Reactions({ messageId, reactions }: ReactionsProps) {
  const { toggleReaction } = useChatSessionStore();
  const myId = useAuthStore((s) => s.user?.ID);

  return (
    <div className="reacts">
      {reactions.map((r) => {
        const isMine = myId ? r.users.includes(myId) : false;
        return (
          <button
            key={r.emoji}
            className={`react ${isMine ? "on" : ""}`}
            onClick={() => toggleReaction(messageId, r.emoji)}
          >
            <span>{r.emoji}</span>
            <span className="count">{r.count}</span>
          </button>
        );
      })}
    </div>
  );
}
