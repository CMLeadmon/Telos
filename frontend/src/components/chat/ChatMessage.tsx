"use client";

import React, { useState } from "react";
import { Smile, Edit2, Trash2, Pin } from "lucide-react";
import { ChatMessage as ChatMessageType, useChatSessionStore } from "@/stores/useChatSessionStore";
import { useAuthStore } from "@/stores/useAuthStore";
import { apiBase } from "@/lib/api";
import { Reactions } from "./Reactions";

const AVATAR_HUES = ["--rose", "--cyan", "--violet", "--indigo", "--azure"];

export function avatarHue(name: string): string {
  let h = 0;
  for (const ch of name) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  return `var(${AVATAR_HUES[h % AVATAR_HUES.length]})`;
}

interface ChatMessageProps {
  message: ChatMessageType;
  canModerate: boolean;
}

export function ChatMessage({ message, canModerate }: ChatMessageProps) {
  const { editMessage, deleteMessage, toggleReaction } = useChatSessionStore();
  const currentUser = useAuthStore((s) => s.user);
  const myId = currentUser?.ID;
  const isOwn = message.senderId === myId;

  const [isEditing, setIsEditing] = useState(false);
  const [editContent, setEditContent] = useState(message.content);
  const [showQuickReactions, setShowQuickReactions] = useState(false);

  const quickEmojis = ["🔥", "📖", "🌊", "✅", "👍", "😂"];

  const handleEdit = async () => {
    if (editContent.trim() && editContent !== message.content) {
      await editMessage(message.id, editContent);
    }
    setIsEditing(false);
  };

  const handleDelete = async () => {
    if (confirm("Are you sure you want to delete this message?")) {
      await deleteMessage(message.id);
    }
  };

  const handleQuickReact = async (emoji: string) => {
    await toggleReaction(message.id, emoji);
    setShowQuickReactions(false);
  };

  if (message.deleted) {
    return (
      <div className="msg deleted-msg">
        <div className="av" style={{ background: "var(--surface-3)", color: "var(--faint)" }}>
          ✖
        </div>
        <div style={{ minWidth: 0, flex: 1 }}>
          <div className="mhead">
            <span className="mname" style={{ color: "var(--faint)" }}>[deleted]</span>
            <span className="mtime">{message.timestamp}</span>
          </div>
          <p className="mbody deleted">message deleted</p>
        </div>
      </div>
    );
  }

  return (
    <div className="msg">
      {/* Hover Action Bar */}
      <div className="msgactions">
        <div style={{ position: "relative" }}>
          <button
            className="iconbtn action-btn"
            title="React"
            onClick={() => setShowQuickReactions(!showQuickReactions)}
          >
            <Smile size={16} />
          </button>
          {showQuickReactions && (
            <div className="quick-react-popup">
              {quickEmojis.map((emoji) => (
                <button
                  key={emoji}
                  className="quick-react-btn"
                  onClick={() => handleQuickReact(emoji)}
                >
                  {emoji}
                </button>
              ))}
            </div>
          )}
        </div>
        {isOwn && (
          <button className="iconbtn action-btn" title="Edit" onClick={() => setIsEditing(true)}>
            <Edit2 size={16} />
          </button>
        )}
        {(isOwn || canModerate) && (
          <button className="iconbtn action-btn text-rose" title="Delete" onClick={handleDelete}>
            <Trash2 size={16} />
          </button>
        )}
      </div>

      <div className="av" style={{ background: avatarHue(message.sender) }}>
        {message.avatarUrl ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img className="msgav" src={`${apiBase()}${message.avatarUrl}`} alt="" />
        ) : (
          message.avatar
        )}
      </div>

      <div style={{ minWidth: 0, flex: 1 }}>
        <div className="mhead">
          <span className="mname">{message.displayName || message.sender}</span>
          <span className="rolepill">{message.role}</span>
          <span className="mtime">{message.timestamp}</span>
          {message.editedAt && <span className="mtime">(edited)</span>}
          {message.pinned && <span className="pin-badge" title="Pinned"><Pin size={10} className="inline-block fill-current" /></span>}
        </div>

        {isEditing ? (
          <div className="edit-box">
            <input
              className="edit-input"
              value={editContent}
              onChange={(e) => setEditContent(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") handleEdit();
                if (e.key === "Escape") setIsEditing(false);
              }}
              autoFocus
            />
            <div className="edit-actions">
              <button className="btn-save" onClick={handleEdit}>Save</button>
              <button className="btn-cancel" onClick={() => setIsEditing(false)}>Cancel</button>
            </div>
          </div>
        ) : (
          <p className="mbody">{message.content}</p>
        )}

        {/* Reactions list */}
        <div className="reactions-container-wrapper">
          {message.reactions && message.reactions.length > 0 ? (
            <div className="reactions-row">
              <Reactions messageId={message.id} reactions={message.reactions} />
              <button
                className="react add"
                onClick={() => setShowQuickReactions(!showQuickReactions)}
                title="Add Reaction"
              >
                ＋
              </button>
            </div>
          ) : null}
        </div>
      </div>
    </div>
  );
}
