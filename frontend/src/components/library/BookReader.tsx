"use client";

import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { X } from "lucide-react";
import type { LibraryBook } from "@/stores/useLibraryStore";
import { useAuthStore } from "@/stores/useAuthStore";
import { useAnnotationStore, type AnnotationLocator } from "@/stores/useAnnotationStore";
import { formatKind } from "@/lib/reader";
import { EpubReader } from "./EpubReader";
import { PdfReader } from "./PdfReader";
import { AnnotationPanel } from "./AnnotationPanel";
import { AnnotationEditor } from "./AnnotationEditor";

interface PendingSelection {
  locator: AnnotationLocator;
  text: string;
}

// BookReader is the overlay chrome that dispatches to the format-specific
// in-app reader and hosts the annotation panel. Neither reader library loads
// until its reader mounts.
export function BookReader({
  book,
  onClose,
}: {
  book: LibraryBook;
  onClose: () => void;
}) {
  const [percent, setPercent] = useState(0);
  const [pending, setPending] = useState<PendingSelection | null>(null);
  const kind = formatKind(book.format);
  const canModerate = useAuthStore((s) => s.user?.Permissions?.includes("moderate_annotations") ?? false);
  const createAnnotation = useAnnotationStore((s) => s.create);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const onSelection = (locator: AnnotationLocator, text: string) => {
    if (text.trim()) setPending({ locator, text });
  };

  return createPortal(
    <div className="reader-overlay" data-testid="book-reader" role="dialog" aria-label={`reading ${book.title}`}>
      <header className="reader-bar">
        <span className="reader-title">{book.title}</span>
        <span className="reader-percent" data-testid="reader-percent">
          {Math.round(percent * 100)}%
        </span>
        <button className="iconbtn" aria-label="close reader" onClick={onClose}>
          <X size={18} />
        </button>
      </header>

      <div className="reader-body">
        <div className="reader-main">
          {kind === "epub" && (
            <EpubReader book={book} onClose={onClose} onPercent={setPercent} onSelection={onSelection} />
          )}
          {kind === "pdf" && <PdfReader book={book} onPercent={setPercent} onSelection={onSelection} />}
          {kind === null && (
            <p className="reader-error" data-testid="reader-unsupported">
              This book format cannot be read in the app.
            </p>
          )}
        </div>

        {kind !== null && (
          <div className="reader-side">
            {pending && (
              <AnnotationEditor
                selectedText={pending.text}
                onSave={async (note, visibility) => {
                  await createAnnotation({ locator: pending.locator, selectedText: pending.text, note, visibility });
                  setPending(null);
                }}
                onCancel={() => setPending(null)}
              />
            )}
            <AnnotationPanel bookId={book.id} canModerate={canModerate} />
          </div>
        )}
      </div>
    </div>,
    document.body,
  );
}
