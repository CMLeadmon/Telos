"use client";

import React from "react";
import { apiBase } from "@/lib/api";

export interface SearchUser {
  id: string;
  username: string;
  displayName: string;
  avatarUrl?: string;
}

interface MentionAutocompleteProps {
  users: SearchUser[];
  selectedIndex: number;
  onSelect: (user: SearchUser) => void;
}

export function MentionAutocomplete({ users, selectedIndex, onSelect }: MentionAutocompleteProps) {
  if (users.length === 0) return null;

  return (
    <div
      className="mention-dropdown"
      style={{
        position: "absolute",
        bottom: "100%",
        left: "50px",
        width: "260px",
        maxHeight: "220px",
        overflowY: "auto",
        background: "var(--surface-2)",
        border: "1px solid var(--line)",
        borderRadius: "var(--r)",
        boxShadow: "0 -4px 12px rgba(0, 0, 0, 0.15)",
        zIndex: 1000,
        display: "flex",
        flexDirection: "column",
        padding: "4px",
      }}
    >
      {users.map((user, idx) => {
        const active = idx === selectedIndex;
        return (
          <button
            key={user.id}
            onClick={() => onSelect(user)}
            style={{
              display: "flex",
              alignItems: "center",
              gap: "8px",
              padding: "6px 10px",
              background: active ? "var(--surface-3)" : "transparent",
              border: "none",
              textAlign: "left",
              color: active ? "var(--accent)" : "var(--ink)",
              cursor: "pointer",
              borderRadius: "var(--r-xs)",
              width: "100%",
              outline: "none",
            }}
          >
            <div
              style={{
                width: "24px",
                height: "24px",
                borderRadius: "50%",
                background: "var(--surface-3)",
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                fontSize: "10px",
                fontWeight: 600,
                overflow: "hidden",
                flex: "none",
              }}
            >
              {user.avatarUrl ? (
                // eslint-disable-next-line @next/next/no-img-element
                <img
                  src={`${apiBase()}${user.avatarUrl}`}
                  alt=""
                  style={{ width: "100%", height: "100%", objectFit: "cover" }}
                />
              ) : (
                user.username.slice(0, 2).toUpperCase()
              )}
            </div>
            <div style={{ minWidth: 0, flex: 1 }}>
              <div
                style={{
                  fontSize: "12px",
                  fontWeight: 600,
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                }}
              >
                {user.displayName || user.username}
              </div>
              {user.displayName && (
                <div
                  style={{
                    fontSize: "10px",
                    color: "var(--faint)",
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                  }}
                >
                  @{user.username}
                </div>
              )}
            </div>
          </button>
        );
      })}
    </div>
  );
}
