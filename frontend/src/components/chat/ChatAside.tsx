"use client";

import React from "react";
import { useChatSessionStore } from "@/stores/useChatSessionStore";
import { avatarHue } from "./ChatMessage";
import { apiBase } from "@/lib/api";

export function ChatAside() {
  const { online, onlineCount, messages } = useChatSessionStore();
  const pins = messages.filter((m) => m.pinned && !m.deleted);

  return (
    <aside className="aside">
      <div>
        <h3>Pinned Messages</h3>
        <div className="chanlist" style={{ gap: "8px" }}>
          {pins.length === 0 ? (
            <div className="specrow" style={{ fontStyle: "italic", color: "var(--faint)" }}>
              No pinned messages
            </div>
          ) : (
            pins.map((pin) => (
              <div
                key={pin.id}
                className="pin"
                style={{
                  display: "flex",
                  flexDirection: "column",
                  gap: "4px",
                  padding: "8px",
                  border: "1px solid var(--line-2)",
                  borderRadius: "var(--r-xs)",
                }}
              >
                <div
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: "6px",
                    fontSize: "11px",
                    color: "var(--faint)",
                  }}
                >
                  <span style={{ fontWeight: 600, color: "var(--muted)" }}>
                    {pin.displayName || pin.sender}
                  </span>
                  <span>•</span>
                  <span>{pin.timestamp}</span>
                </div>
                <div
                  style={{
                    fontSize: "12px",
                    color: "var(--muted)",
                    textOverflow: "ellipsis",
                    overflow: "hidden",
                    whiteSpace: "nowrap",
                  }}
                >
                  {pin.content}
                </div>
              </div>
            ))
          )}
        </div>
      </div>

      <div>
        <h3>Channel Stats</h3>
        <div className="specrow">
          <span>Active Roster</span>
          <b>{online.length}</b>
        </div>
      </div>

      <div>
        <h3>Online Members ({onlineCount})</h3>
        <div className="chanlist" style={{ gap: "4px" }}>
          {online.map((u) => {
            const isOracle =
              u.username.toLowerCase() === "oracle" || u.role.toLowerCase() === "ai oracle";
            return (
              <div
                key={u.userId}
                className="memrow"
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: "10px",
                  padding: "6px 8px",
                  borderRadius: "var(--r-xs)",
                }}
              >
                <div
                  className="av"
                  style={{
                    background: avatarHue(u.username),
                    width: "28px",
                    height: "28px",
                    fontSize: "10px",
                    flex: "none",
                  }}
                >
                  {u.avatarUrl ? (
                    // eslint-disable-next-line @next/next/no-img-element
                    <img
                      className="msgav"
                      src={`${apiBase()}${u.avatarUrl}`}
                      alt=""
                      style={{
                        width: "100%",
                        height: "100%",
                        objectFit: "cover",
                        borderRadius: "inherit",
                      }}
                    />
                  ) : (
                    u.avatar
                  )}
                </div>
                <div style={{ minWidth: 0, flex: 1 }}>
                  <div
                    style={{
                      fontSize: "13px",
                      fontWeight: 500,
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                    }}
                  >
                    {u.displayName || u.username}
                  </div>
                  <div
                    style={{
                      fontSize: "10px",
                      color: "var(--faint)",
                      textTransform: "uppercase",
                      letterSpacing: "0.05em",
                    }}
                  >
                    {u.role}
                  </div>
                </div>
                <div
                  className="dot"
                  style={{
                    width: "8px",
                    height: "8px",
                    borderRadius: "50%",
                    background: isOracle ? "var(--cyan)" : "var(--rose)",
                  }}
                />
              </div>
            );
          })}
        </div>
      </div>
    </aside>
  );
}
