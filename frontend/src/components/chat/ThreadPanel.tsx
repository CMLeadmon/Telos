"use client";

import { useChatSessionStore } from "@/stores/useChatSessionStore";
import { ThreadComposer } from "./ThreadComposer";

// ThreadPanel is a single-level thread view: it keeps the root (and thus channel
// context) visible above the ascending replies, and offers a reply composer.
// Replies themselves carry no further reply control — threads are one level.
export function ThreadPanel() {
  const root = useChatSessionStore((s) => s.activeThreadRoot);
  const replies = useChatSessionStore((s) => s.threadReplies);
  const status = useChatSessionStore((s) => s.threadStatus);
  const nextCursor = useChatSessionStore((s) => s.threadNextCursor);
  const loadMore = useChatSessionStore((s) => s.loadMoreReplies);
  const close = useChatSessionStore((s) => s.closeThread);

  if (!root) return null;

  return (
    <aside className="thread-panel" role="complementary" aria-label="Thread" data-testid="thread-panel">
      <header className="thread-head">
        <h3>Thread</h3>
        <button className="thread-close" aria-label="Close thread" onClick={close}>
          ×
        </button>
      </header>

      {/* Root stays visible so channel context is never lost. */}
      <div className="thread-root" data-testid="thread-root">
        <span className="thread-author">{root.displayName || root.sender}</span>
        <span className="thread-content">{root.content}</span>
      </div>

      <div className="thread-replies">
        {nextCursor && (
          <button className="thread-more" onClick={() => void loadMore()} disabled={status === "loading"}>
            {status === "loading" ? "Loading…" : "Load earlier replies"}
          </button>
        )}
        {replies.length === 0 && status === "ready" ? (
          <p className="thread-empty">No replies yet.</p>
        ) : (
          <ul>
            {replies.map((m) => (
              <li key={m.id} className="thread-reply" data-testid={`thread-reply-${m.id}`}>
                <span className="thread-author">{m.displayName || m.sender}</span>
                <span className="thread-content">{m.content}</span>
              </li>
            ))}
          </ul>
        )}
      </div>

      <ThreadComposer />
    </aside>
  );
}
