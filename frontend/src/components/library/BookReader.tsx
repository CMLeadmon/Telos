"use client";

import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { X } from "lucide-react";
import type { LibraryBook } from "@/stores/useLibraryStore";
import { formatKind } from "@/lib/reader";
import { EpubReader } from "./EpubReader";
import { PdfReader } from "./PdfReader";

// BookReader is the overlay chrome that dispatches to the format-specific
// in-app reader by the book's server-reported format. Neither reader library
// loads until its reader mounts.
export function BookReader({
  book,
  onClose,
}: {
  book: LibraryBook;
  onClose: () => void;
}) {
  const [percent, setPercent] = useState(0);
  const kind = formatKind(book.format);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

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

      {kind === "epub" && <EpubReader book={book} onClose={onClose} onPercent={setPercent} />}
      {kind === "pdf" && <PdfReader book={book} onPercent={setPercent} />}
      {kind === null && (
        <p className="reader-error" data-testid="reader-unsupported">
          This book format cannot be read in the app.
        </p>
      )}
    </div>,
    document.body,
  );
}
