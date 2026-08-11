"use client";

import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { ImageOff, X } from "lucide-react";
import { api, assetUrl, libraryCoverUrl } from "@/lib/api";
import {
  asList,
  type LibraryBook,
  useLibraryStore,
} from "@/stores/useLibraryStore";

interface LibraryMetadata {
  title: string;
  subtitle: string;
  authors: string[];
  categories: string[];
  language: string;
  description: string;
  seriesName: string;
  seriesNumber: number | null;
  publisher: string;
  publishedDate: string;
  isbn10: string;
  isbn13: string;
}

interface MetadataCandidate extends LibraryMetadata {
  provider: string;
  coverUrl: string;
}

interface ManageForm {
  title: string;
  subtitle: string;
  authors: string;
  categories: string;
  language: string;
  description: string;
  seriesName: string;
  seriesNumber: string;
  publisher: string;
  publishedDate: string;
  isbn10: string;
  isbn13: string;
}

type MetadataField = keyof LibraryMetadata;

const FIELDS: { key: MetadataField; label: string }[] = [
  { key: "title", label: "Title" },
  { key: "subtitle", label: "Subtitle" },
  { key: "authors", label: "Authors" },
  { key: "categories", label: "Categories" },
  { key: "language", label: "Language" },
  { key: "description", label: "Description" },
  { key: "seriesName", label: "Series name" },
  { key: "seriesNumber", label: "Series number" },
  { key: "publisher", label: "Publisher" },
  { key: "publishedDate", label: "Published date" },
  { key: "isbn10", label: "ISBN-10" },
  { key: "isbn13", label: "ISBN-13" },
];

function bookToForm(book: LibraryBook): ManageForm {
  return {
    title: book.title ?? "",
    subtitle: book.subtitle ?? "",
    authors: asList(book.authors).join(", "),
    categories: asList(book.categories).join(", "),
    language: book.language ?? "",
    description: book.description ?? "",
    seriesName: book.seriesName ?? "",
    seriesNumber:
      book.seriesNumber === null || book.seriesNumber === undefined
        ? ""
        : String(book.seriesNumber),
    publisher: book.publisher ?? "",
    publishedDate: book.publishedDate ?? "",
    isbn10: book.isbn10 ?? "",
    isbn13: book.isbn13 ?? "",
  };
}

function commaList(value: string): string[] {
  return value
    .split(",")
    .map((part) => part.trim())
    .filter(Boolean);
}

function formToMetadata(form: ManageForm): LibraryMetadata {
  const seriesNumber = Number.parseFloat(form.seriesNumber);
  return {
    ...form,
    authors: commaList(form.authors),
    categories: commaList(form.categories),
    seriesNumber: Number.isFinite(seriesNumber) ? seriesNumber : null,
  };
}

function displayValue(value: LibraryMetadata[MetadataField]): string {
  if (Array.isArray(value)) return value.join(", ");
  if (value === null || value === undefined) return "—";
  return String(value) || "—";
}

function candidateHasValue(
  candidate: MetadataCandidate,
  field: MetadataField,
): boolean {
  const value = candidate[field];
  if (Array.isArray(value)) return value.length > 0;
  return value !== null && value !== undefined && String(value).trim() !== "";
}

export function BookManageModal({
  book,
  onClose,
}: {
  book: LibraryBook;
  onClose: () => void;
}) {
  const fetchCatalog = useLibraryStore((state) => state.fetchCatalog);
  const [freshBook, setFreshBook] = useState(book);
  const [form, setForm] = useState<ManageForm>(() => bookToForm(book));
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [fetching, setFetching] = useState(false);
  const [coverBusy, setCoverBusy] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const [candidates, setCandidates] = useState<MetadataCandidate[]>([]);
  const [selectedCandidate, setSelectedCandidate] =
    useState<MetadataCandidate | null>(null);
  const [applyFields, setApplyFields] = useState<
    Partial<Record<MetadataField, boolean>>
  >({});
  const [coverFile, setCoverFile] = useState<File | null>(null);
  const [coverRevision, setCoverRevision] = useState(0);
  const [coverBroken, setCoverBroken] = useState(false);

  useEffect(() => {
    let disposed = false;
    api<LibraryBook>(
      `/api/v1/library/books/${encodeURIComponent(book.id)}`,
    )
      .then((loaded) => {
        if (disposed) return;
        setFreshBook(loaded);
        setForm(bookToForm(loaded));
        setLoading(false);
      })
      .catch((reason) => {
        if (disposed) return;
        setLoading(false);
        setError(
          reason instanceof Error ? reason.message : "failed to load book",
        );
      });
    return () => {
      disposed = true;
    };
  }, [book.id]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [onClose]);

  const saveMetadata = async () => {
    setSaving(true);
    setError(null);
    setMessage(null);
    try {
      const updated = await api<LibraryBook>(
        `/api/v1/library/books/${encodeURIComponent(book.id)}/metadata`,
        { method: "PUT", body: JSON.stringify(formToMetadata(form)) },
      );
      setFreshBook(updated);
      setForm(bookToForm(updated));
      setMessage("Metadata saved.");
      await fetchCatalog();
    } catch (reason) {
      setError(
        reason instanceof Error ? reason.message : "failed to save metadata",
      );
    } finally {
      setSaving(false);
    }
  };

  const fetchMetadata = async () => {
    setFetching(true);
    setError(null);
    setMessage(null);
    try {
      const result = await api<{ candidates: MetadataCandidate[] }>(
        `/api/v1/library/books/${encodeURIComponent(book.id)}/metadata/fetch`,
        { method: "POST" },
      );
      setCandidates(result.candidates ?? []);
      setSelectedCandidate(null);
      setApplyFields({});
      if ((result.candidates ?? []).length === 0) {
        setMessage("No metadata candidates were found.");
      }
    } catch (reason) {
      setError(
        reason instanceof Error ? reason.message : "metadata fetch failed",
      );
    } finally {
      setFetching(false);
    }
  };

  const chooseCandidate = (candidate: MetadataCandidate) => {
    const defaults: Partial<Record<MetadataField, boolean>> = {};
    for (const { key } of FIELDS) {
      defaults[key] = candidateHasValue(candidate, key);
    }
    setSelectedCandidate(candidate);
    setApplyFields(defaults);
  };

  const applyCandidate = () => {
    if (!selectedCandidate) return;
    setForm((current) => {
      const next = { ...current };
      for (const { key } of FIELDS) {
        if (!applyFields[key]) continue;
        const value = selectedCandidate[key];
        if (key === "authors" || key === "categories") {
          next[key] = Array.isArray(value) ? value.join(", ") : "";
        } else if (key === "seriesNumber") {
          next.seriesNumber = value === null ? "" : String(value);
        } else {
          next[key] = typeof value === "string" ? value : "";
        }
      }
      return next;
    });
    setMessage("Candidate fields applied. Save to persist them.");
  };

  const replaceCover = async (body: FormData | { coverUrl: string }) => {
    setCoverBusy(true);
    setError(null);
    setMessage(null);
    try {
      await api<void>(`/api/v1/library/books/${encodeURIComponent(book.id)}/cover`, {
        method: "PUT",
        body: body instanceof FormData ? body : JSON.stringify(body),
      });
      setCoverRevision(Date.now());
      setCoverBroken(false);
      setCoverFile(null);
      setMessage("Cover replaced.");
    } catch (reason) {
      setError(
        reason instanceof Error ? reason.message : "cover replacement failed",
      );
    } finally {
      setCoverBusy(false);
    }
  };

  const uploadCover = () => {
    if (!coverFile) return;
    const body = new FormData();
    body.append("cover", coverFile);
    void replaceCover(body);
  };

  const deleteBook = async () => {
    const title = form.title || freshBook.title;
    if (
      !window.confirm(
        `Delete “${title}”? This removes the book and its file for everyone.`,
      )
    ) {
      return;
    }
    setDeleting(true);
    setError(null);
    try {
      await api<void>(`/api/v1/library/books/${encodeURIComponent(book.id)}`, {
        method: "DELETE",
      });
      await fetchCatalog();
      onClose();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "delete failed");
      setDeleting(false);
    }
  };

  const currentMetadata = formToMetadata(form);

  return createPortal(
    <div
      className="book-manage-overlay"
      data-testid="book-manage-modal"
      role="dialog"
      aria-modal="true"
      aria-label={`manage ${freshBook.title}`}
    >
      <div className="book-manage-modal">
        <header className="book-manage-bar">
          <div>
            <span className="kicker">{"// shared library"}</span>
            <h2>{freshBook.title}</h2>
          </div>
          <button
            className="iconbtn"
            aria-label="close book settings"
            onClick={onClose}
          >
            <X size={18} />
          </button>
        </header>

        <div className="book-manage-body">
          {error && <p className="book-manage-message error">{error}</p>}
          {message && <p className="book-manage-message">{message}</p>}

          <section className="book-manage-section">
            <div className="book-manage-sectionhead">
              <div>
                <span className="kicker">{"// metadata"}</span>
                <h3>Edit book details</h3>
              </div>
              <button
                className="btn-ghost btn-sm"
                disabled={loading || fetching}
                onClick={() => void fetchMetadata()}
              >
                {fetching ? "fetching…" : "Fetch metadata"}
              </button>
            </div>

            <div className="book-manage-form">
              {FIELDS.filter(
                ({ key }) => key !== "description" && key !== "seriesNumber",
              ).map(({ key, label }) => (
                <label key={key}>
                  <span>{label}</span>
                  <input
                    type={key === "publishedDate" ? "date" : "text"}
                    value={form[key]}
                    disabled={loading}
                    onChange={(event) =>
                      setForm((current) => ({
                        ...current,
                        [key]: event.target.value,
                      }))
                    }
                  />
                </label>
              ))}
              <label>
                <span>Series number</span>
                <input
                  type="number"
                  step="any"
                  value={form.seriesNumber}
                  disabled={loading}
                  onChange={(event) =>
                    setForm((current) => ({
                      ...current,
                      seriesNumber: event.target.value,
                    }))
                  }
                />
              </label>
              <label className="book-manage-wide">
                <span>Description</span>
                <textarea
                  rows={5}
                  value={form.description}
                  disabled={loading}
                  onChange={(event) =>
                    setForm((current) => ({
                      ...current,
                      description: event.target.value,
                    }))
                  }
                />
              </label>
            </div>
            <p className="book-manage-hint">
              Separate authors and categories with commas.
            </p>
            <button
              className="btn btn-sm"
              disabled={loading || saving}
              onClick={() => void saveMetadata()}
            >
              {saving ? "saving…" : "Save metadata"}
            </button>

            {candidates.length > 0 && (
              <div className="metadata-review">
                <div className="candidate-list" aria-label="metadata candidates">
                  {candidates.map((candidate, index) => (
                    <button
                      key={`${candidate.provider}-${index}`}
                      className={
                        selectedCandidate === candidate ? "selected" : ""
                      }
                      onClick={() => chooseCandidate(candidate)}
                    >
                      <strong>{candidate.provider || "Unknown provider"}</strong>
                      <span>{candidate.title || "Untitled candidate"}</span>
                    </button>
                  ))}
                </div>

                {selectedCandidate && (
                  <div className="metadata-compare">
                    <div className="metadata-compare-head">
                      <span>Field</span>
                      <span>Current</span>
                      <span>Candidate</span>
                      <span>Apply</span>
                    </div>
                    {FIELDS.map(({ key, label }) => (
                      <div className="metadata-compare-row" key={key}>
                        <strong>{label}</strong>
                        <span>{displayValue(currentMetadata[key])}</span>
                        <span>{displayValue(selectedCandidate[key])}</span>
                        <input
                          type="checkbox"
                          aria-label={`apply ${label}`}
                          checked={applyFields[key] ?? false}
                          onChange={(event) =>
                            setApplyFields((current) => ({
                              ...current,
                              [key]: event.target.checked,
                            }))
                          }
                        />
                      </div>
                    ))}
                    <button className="btn-ghost btn-sm" onClick={applyCandidate}>
                      Apply selected fields
                    </button>
                  </div>
                )}
              </div>
            )}
          </section>

          <section className="book-manage-section book-cover-section">
            <div>
              <span className="kicker">{"// cover"}</span>
              <h3>Replace artwork</h3>
            </div>
            <div className="book-cover-controls">
              <div className="book-cover-preview">
                {coverBroken ? (
                  <ImageOff size={28} />
                ) : (
                  <img
                    src={assetUrl(`${libraryCoverUrl(book.id)}?v=${coverRevision}`)}
                    alt={`Cover of ${freshBook.title}`}
                    onError={() => setCoverBroken(true)}
                  />
                )}
              </div>
              <div className="book-cover-actions">
                <label>
                  <span>JPEG or PNG, up to 5 MB</span>
                  <input
                    type="file"
                    accept="image/jpeg,image/png"
                    onChange={(event) => {
                      const file = event.target.files?.[0] ?? null;
                      if (file && file.size > 5 * 1024 * 1024) {
                        setCoverFile(null);
                        setError("Cover exceeds 5 MB.");
                        return;
                      }
                      setError(null);
                      setCoverFile(file);
                    }}
                  />
                </label>
                <button
                  className="btn-ghost btn-sm"
                  disabled={!coverFile || coverBusy}
                  onClick={uploadCover}
                >
                  {coverBusy ? "replacing…" : "Upload image"}
                </button>
                {selectedCandidate?.coverUrl && (
                  <button
                    className="btn-ghost btn-sm"
                    disabled={coverBusy}
                    onClick={() =>
                      void replaceCover({
                        coverUrl: selectedCandidate.coverUrl,
                      })
                    }
                  >
                    Use this candidate&apos;s cover
                  </button>
                )}
              </div>
            </div>
          </section>

          <section className="book-manage-section book-danger-zone">
            <div>
              <span className="kicker">{"// danger zone"}</span>
              <h3>Delete from the shared shelf</h3>
              <p>This removes the book and its file for everyone.</p>
            </div>
            <button
              className="btn-danger btn-sm"
              disabled={deleting}
              onClick={() => void deleteBook()}
            >
              {deleting ? "deleting…" : "Delete book"}
            </button>
          </section>
        </div>
      </div>
    </div>,
    document.body,
  );
}
