"use client";

import { useEffect, useRef } from "react";
import { useNotificationStore, type AppNotification } from "@/stores/useNotificationStore";
import { NotificationItem } from "./NotificationItem";

// NotificationInbox is a labeled, keyboard-operable dialog listing the viewer's
// notifications with a server-derived unread badge, mark-one/all, and paging.
export function NotificationInbox({ open, onClose }: { open: boolean; onClose: () => void }) {
  const items = useNotificationStore((s) => s.items);
  const nextCursor = useNotificationStore((s) => s.nextCursor);
  const status = useNotificationStore((s) => s.status);
  const error = useNotificationStore((s) => s.error);
  const loadInitial = useNotificationStore((s) => s.loadInitial);
  const loadMore = useNotificationStore((s) => s.loadMore);
  const markRead = useNotificationStore((s) => s.markRead);
  const markAllRead = useNotificationStore((s) => s.markAllRead);

  const dialogRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (open) {
      void loadInitial();
      dialogRef.current?.focus();
    }
  }, [open, loadInitial]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open) return null;

  const activate = (n: AppNotification) => {
    void markRead(n.id);
  };

  return (
    <div className="notif-overlay" onClick={onClose}>
      <div
        ref={dialogRef}
        className="notif-dialog"
        role="dialog"
        aria-label="Notifications"
        aria-modal="true"
        tabIndex={-1}
        onClick={(e) => e.stopPropagation()}
        data-testid="notification-inbox"
      >
        <header className="notif-head">
          <h2>Notifications</h2>
          <button className="notif-markall" onClick={() => void markAllRead()}>
            Mark all read
          </button>
        </header>

        {error && (
          <div className="notif-error" role="alert">
            {error}{" "}
            <button onClick={() => void loadInitial()}>Retry</button>
          </div>
        )}

        {items.length === 0 && status !== "loading" ? (
          <p className="notif-empty">You have no notifications.</p>
        ) : (
          <ul className="notif-list" role="listbox" aria-label="Notification list">
            {items.map((n) => (
              <NotificationItem key={n.id} notification={n} onActivate={activate} />
            ))}
          </ul>
        )}

        {nextCursor && (
          <button
            className="notif-more"
            onClick={() => void loadMore()}
            disabled={status === "loading"}
          >
            {status === "loading" ? "Loading…" : "Load more"}
          </button>
        )}
      </div>
    </div>
  );
}
