"use client";

import { useState } from "react";
import { useChatSessionStore } from "@/stores/useChatSessionStore";

// ThreadComposer submits a reply, keeping the draft text and its mutation id on
// failure so a retry maps to exactly one durable message. The input clears only
// after acknowledgement.
export function ThreadComposer() {
  const sendReply = useChatSessionStore((s) => s.sendReply);
  const draftError = useChatSessionStore((s) => s.threadDraftError);
  const [text, setText] = useState("");
  const [sending, setSending] = useState(false);

  const submit = async () => {
    const content = text.trim();
    if (!content || sending) return;
    setSending(true);
    try {
      await sendReply(content);
      setText(""); // cleared only after acknowledgement
    } catch {
      // Draft text is preserved for retry; the store holds the error + mutation id.
    } finally {
      setSending(false);
    }
  };

  return (
    <div className="thread-composer">
      {draftError && (
        <div className="thread-error" role="alert">
          {draftError} — your reply was kept; press Enter to retry.
        </div>
      )}
      <input
        aria-label="Reply in thread"
        placeholder="Reply…"
        value={text}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && !e.shiftKey) {
            e.preventDefault();
            void submit();
          }
        }}
        data-testid="thread-reply-input"
      />
      <button onClick={() => void submit()} disabled={sending || !text.trim()}>
        {sending ? "Sending…" : "Reply"}
      </button>
    </div>
  );
}
