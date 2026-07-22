"use client";

import { useState } from "react";
import type { AnnotationVisibility } from "@/stores/useAnnotationStore";

// AnnotationEditor edits a note and its visibility. Visibility defaults to
// private; sharing to the community is an explicit choice.
export function AnnotationEditor({
  initialNote = "",
  initialVisibility = "private",
  selectedText,
  onSave,
  onCancel,
}: {
  initialNote?: string;
  initialVisibility?: AnnotationVisibility;
  selectedText?: string;
  onSave: (note: string, visibility: AnnotationVisibility) => Promise<void>;
  onCancel: () => void;
}) {
  const [note, setNote] = useState(initialNote);
  const [visibility, setVisibility] = useState<AnnotationVisibility>(initialVisibility);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      await onSave(note, visibility);
    } catch {
      // The note text is preserved in local state for retry.
      setError("Could not save — your note was kept; try again.");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="annot-editor" data-testid="annotation-editor">
      {selectedText && <blockquote className="annot-quote">{selectedText}</blockquote>}
      <textarea
        aria-label="Annotation note"
        value={note}
        onChange={(e) => setNote(e.target.value)}
        maxLength={10000}
        placeholder="Add a note…"
        data-testid="annotation-note-input"
      />
      <label className="annot-visibility">
        <input
          type="checkbox"
          checked={visibility === "community"}
          onChange={(e) => setVisibility(e.target.checked ? "community" : "private")}
          data-testid="annotation-share-toggle"
        />
        Share with the community
      </label>
      {error && <p className="annot-error" role="alert">{error}</p>}
      <div className="annot-actions">
        <button onClick={() => void save()} disabled={saving} data-testid="annotation-save">
          {saving ? "Saving…" : "Save"}
        </button>
        <button onClick={onCancel} className="annot-cancel">
          Cancel
        </button>
      </div>
    </div>
  );
}
