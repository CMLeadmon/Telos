"use client";

import { useEffect, useState } from "react";
import { api } from "@/lib/api";
import { useAuthStore } from "@/stores/useAuthStore";
import {
  type Annotation,
  type AnnotationReply,
  type AnnotationVisibility,
} from "@/stores/useAnnotationStore";

// CommentaryPanel is the discussion surface for a streamed media item or a file.
// It reuses the annotation model — a comment is an annotation with an empty
// locator — and the shared reply endpoints, but posts to the target-scoped
// comment routes. The server is authoritative for every access decision; this
// component never grants itself visibility or moderation rights.
export function CommentaryPanel({
  targetType,
  targetId,
  canModerate = false,
}: {
  targetType: "media" | "file";
  targetId: string;
  canModerate?: boolean;
}) {
  const user = useAuthStore((s) => s.user);
  const base =
    targetType === "media"
      ? `/api/v1/media/items/${encodeURIComponent(targetId)}/comments`
      : `/api/v1/files/${encodeURIComponent(targetId)}/comments`;

  const [comments, setComments] = useState<Annotation[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [note, setNote] = useState("");
  const [visibility, setVisibility] = useState<AnnotationVisibility>("community");
  const [posting, setPosting] = useState(false);

  // Promise-chain loader: state is only set inside .then/.catch, never
  // synchronously in the effect body (the setState-in-effect lint rule).
  useEffect(() => {
    api<{ annotations: Annotation[] }>(base)
      .then((res) => {
        setComments(res.annotations ?? []);
        setError(null);
      })
      .catch(() => setError("Could not load commentary."));
  }, [base]);

  const post = async () => {
    const body = note.trim();
    if (!body || posting) return;
    setPosting(true);
    try {
      const created = await api<Annotation>(base, {
        method: "POST",
        body: JSON.stringify({ visibility, note: body }),
      });
      setComments((c) => [created, ...c]);
      setNote("");
      setError(null);
    } catch {
      setError("Could not post — your comment was kept.");
    } finally {
      setPosting(false);
    }
  };

  const remove = async (id: string) => {
    try {
      await api(`/api/v1/library/annotations/${id}`, { method: "DELETE" });
      setComments((c) => c.filter((a) => a.id !== id));
    } catch {
      setError("Could not remove the comment.");
    }
  };

  return (
    <section className="commentary" data-testid="commentary-panel" aria-label="Commentary">
      <header className="commentary-head">
        <h3>Commentary</h3>
        <span className="commentary-count">
          {comments.length} comment{comments.length === 1 ? "" : "s"}
        </span>
      </header>

      <div className="commentary-composer">
        <textarea
          aria-label="Write a comment"
          value={note}
          onChange={(e) => setNote(e.target.value)}
          placeholder="Add to the discussion…"
          data-testid="commentary-input"
          rows={2}
        />
        <div className="commentary-actions">
          <label className="commentary-vis">
            <input
              type="checkbox"
              checked={visibility === "community"}
              onChange={(e) => setVisibility(e.target.checked ? "community" : "private")}
            />
            Share with the community
          </label>
          <button
            className="btn btn-sm"
            disabled={posting || note.trim() === ""}
            onClick={() => void post()}
            data-testid="commentary-post"
          >
            {posting ? "Posting…" : "Post"}
          </button>
        </div>
      </div>

      {error && (
        <p className="commentary-error" role="alert">
          {error}
        </p>
      )}

      <ul className="commentary-list">
        {comments.map((a) => (
          <CommentRow
            key={a.id}
            comment={a}
            isOwner={a.ownerId === user?.ID}
            canModerate={canModerate}
            onDelete={() => remove(a.id)}
          />
        ))}
        {comments.length === 0 && (
          <li className="commentary-empty">No comments yet. Start the conversation.</li>
        )}
      </ul>
    </section>
  );
}

function CommentRow({
  comment,
  isOwner,
  canModerate,
  onDelete,
}: {
  comment: Annotation;
  isOwner: boolean;
  canModerate: boolean;
  onDelete: () => Promise<void>;
}) {
  const [replies, setReplies] = useState<AnnotationReply[]>([]);
  const [replyText, setReplyText] = useState("");
  const [replyError, setReplyError] = useState<string | null>(null);
  const isCommunity = comment.visibility === "community";

  useEffect(() => {
    if (!isCommunity) return;
    api<{ replies: AnnotationReply[] }>(`/api/v1/library/annotations/${comment.id}/replies`)
      .then((res) => setReplies(res.replies ?? []))
      .catch(() => {
        /* replies are best-effort; a load failure leaves the thread empty */
      });
  }, [comment.id, isCommunity]);

  const sendReply = async () => {
    const body = replyText.trim();
    if (!body) return;
    setReplyError(null);
    try {
      const created = await api<AnnotationReply>(
        `/api/v1/library/annotations/${comment.id}/replies`,
        { method: "POST", body: JSON.stringify({ body }) },
      );
      setReplies((r) => [...r, created]);
      setReplyText("");
    } catch {
      setReplyError("Could not post — your reply was kept.");
    }
  };

  return (
    <li className="commentary-item" data-testid={`comment-${comment.id}`}>
      <p className="commentary-note">{comment.note}</p>
      <div className="commentary-meta">
        <span className={`commentary-vis-badge ${comment.visibility}`}>
          {comment.visibility}
        </span>
        {(isOwner || canModerate) && (
          <button
            className="commentary-remove"
            onClick={() => void onDelete()}
            data-testid={`comment-delete-${comment.id}`}
          >
            {isOwner ? "Delete" : "Remove"}
          </button>
        )}
      </div>

      {isCommunity && (
        <div className="commentary-replies">
          {replies.map((rp) => (
            <p key={rp.id} className="commentary-reply">
              {rp.body}
            </p>
          ))}
          <input
            aria-label="Reply to comment"
            value={replyText}
            onChange={(e) => setReplyText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") void sendReply();
            }}
            placeholder="Reply…"
            data-testid={`comment-reply-input-${comment.id}`}
          />
          {replyError && <span className="commentary-error">{replyError}</span>}
        </div>
      )}
    </li>
  );
}
