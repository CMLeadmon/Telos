"use client";

import { useEffect, useState } from "react";
import { useAnnotationStore, type Annotation } from "@/stores/useAnnotationStore";
import { useAuthStore } from "@/stores/useAuthStore";
import { AnnotationEditor } from "./AnnotationEditor";

// AnnotationPanel lists a book's annotations with mine/community filters,
// owner edit/delete, capability-driven moderation, and community replies. It
// never derives authorization from client fields — the server is authoritative.
export function AnnotationPanel({ bookId, canModerate }: { bookId: number; canModerate: boolean }) {
  const user = useAuthStore((s) => s.user);
  const annotations = useAnnotationStore((s) => s.annotations);
  const error = useAnnotationStore((s) => s.error);
  const activeId = useAnnotationStore((s) => s.activeId);
  const load = useAnnotationStore((s) => s.load);
  const update = useAnnotationStore((s) => s.update);
  const remove = useAnnotationStore((s) => s.remove);

  const [filter, setFilter] = useState<"mine" | "community">("mine");
  const [editing, setEditing] = useState<string | null>(null);

  useEffect(() => {
    void load(bookId);
  }, [bookId, load]);

  const visible = annotations.filter((a) =>
    filter === "mine" ? a.ownerId === user?.ID : a.visibility === "community",
  );

  return (
    <aside className="annot-panel" data-testid="annotation-panel" aria-label="Annotations">
      <header className="annot-head">
        <h3>Annotations</h3>
        <div className="annot-filters" role="tablist">
          <button role="tab" aria-selected={filter === "mine"} onClick={() => setFilter("mine")}>
            Mine
          </button>
          <button role="tab" aria-selected={filter === "community"} onClick={() => setFilter("community")}>
            Community
          </button>
        </div>
      </header>

      {error && <p className="annot-error" role="alert">{error}</p>}

      <ul className="annot-list">
        {visible.map((a) => (
          <AnnotationRow
            key={a.id}
            a={a}
            isOwner={a.ownerId === user?.ID}
            canModerate={canModerate}
            focused={a.id === activeId}
            editing={editing === a.id}
            onEdit={() => setEditing(a.id)}
            onStopEdit={() => setEditing(null)}
            onSave={(note, visibility) => update(a.id, { note, visibility }).then(() => setEditing(null))}
            onDelete={() => remove(a.id)}
          />
        ))}
        {visible.length === 0 && <li className="annot-empty">No annotations here yet.</li>}
      </ul>
    </aside>
  );
}

function AnnotationRow({
  a,
  isOwner,
  canModerate,
  focused,
  editing,
  onEdit,
  onStopEdit,
  onSave,
  onDelete,
}: {
  a: Annotation;
  isOwner: boolean;
  canModerate: boolean;
  focused: boolean;
  editing: boolean;
  onEdit: () => void;
  onStopEdit: () => void;
  onSave: (note: string, visibility: "private" | "community") => Promise<void>;
  onDelete: () => Promise<void>;
}) {
  const replies = useAnnotationStore((s) => s.replies[a.id]);
  const loadReplies = useAnnotationStore((s) => s.loadReplies);
  const reply = useAnnotationStore((s) => s.reply);
  const [replyText, setReplyText] = useState("");
  const [replyError, setReplyError] = useState<string | null>(null);

  useEffect(() => {
    if (a.visibility === "community") void loadReplies(a.id);
  }, [a.id, a.visibility, loadReplies]);

  if (editing) {
    return (
      <li className="annot-row">
        <AnnotationEditor
          initialNote={a.note}
          initialVisibility={a.visibility}
          selectedText={a.selectedText}
          onSave={onSave}
          onCancel={onStopEdit}
        />
      </li>
    );
  }

  const sendReply = async () => {
    const body = replyText.trim();
    if (!body) return;
    setReplyError(null);
    try {
      await reply(a.id, body);
      setReplyText("");
    } catch {
      setReplyError("Could not post — your reply was kept.");
    }
  };

  return (
    <li className={`annot-row${focused ? " focused" : ""}`} data-testid={`annotation-${a.id}`}>
      <blockquote className="annot-quote">{a.selectedText}</blockquote>
      {a.note && <p className="annot-note">{a.note}</p>}
      <div className="annot-meta">
        <span className={`annot-vis ${a.visibility}`}>{a.visibility}</span>
        {isOwner && (
          <>
            <button onClick={onEdit} data-testid={`annotation-edit-${a.id}`}>
              Edit
            </button>
            <button onClick={() => void onDelete()} data-testid={`annotation-delete-${a.id}`}>
              Delete
            </button>
          </>
        )}
        {!isOwner && canModerate && (
          <button onClick={() => void onDelete()} data-testid={`annotation-moderate-${a.id}`}>
            Remove
          </button>
        )}
      </div>

      {a.visibility === "community" && (
        <div className="annot-replies">
          {(replies ?? []).map((rp) => (
            <p key={rp.id} className="annot-reply">
              {rp.body}
            </p>
          ))}
          <div className="annot-reply-composer">
            <input
              aria-label="Reply to annotation"
              value={replyText}
              onChange={(e) => setReplyText(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") void sendReply();
              }}
              placeholder="Reply…"
              data-testid={`annotation-reply-input-${a.id}`}
            />
            {replyError && <span className="annot-error">{replyError}</span>}
          </div>
        </div>
      )}
    </li>
  );
}
