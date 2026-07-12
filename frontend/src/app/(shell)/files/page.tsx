"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import {
  BookOpen,
  Check,
  ChevronLeft,
  ChevronRight,
  Download,
  File as FileIcon,
  FileText,
  Film,
  Folder,
  Image as ImageIcon,
  Music,
  Trash2,
  UploadCloud,
  X,
} from "lucide-react";
import { apiBase } from "@/lib/api";
import {
  type FileEntry,
  type UploadDestination,
  useFilesStore,
  validateFile,
} from "@/stores/useFilesStore";
import { VaporwaveScene } from "@/components/VaporwaveScene";

function mimeIcon(mime: string) {
  if (mime.startsWith("image/")) return <ImageIcon size={17} />;
  if (mime.startsWith("audio/")) return <Music size={17} />;
  if (mime.startsWith("video/")) return <Film size={17} />;
  if (mime === "application/pdf" || mime === "application/epub+zip")
    return <FileText size={17} />;
  return <FileIcon size={17} />;
}

function humanSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KiB", "MiB", "GiB"];
  let v = bytes / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v >= 10 ? Math.round(v) : v.toFixed(1)} ${units[i]}`;
}

function shortDate(iso: string): string {
  return new Date(iso).toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
}

function FileRow({
  file,
  confirming,
  onAskDelete,
  onCancelDelete,
  onDelete,
}: {
  file: FileEntry;
  confirming: boolean;
  onAskDelete: () => void;
  onCancelDelete: () => void;
  onDelete: () => void;
}) {
  const infected = file.scan_status !== "clean";
  return (
    <div
      className={`frow${infected ? " infected" : ""}`}
      data-testid="file-row"
    >
      <span className="ficon">{mimeIcon(file.mime_type)}</span>
      <span className="fname" title={file.filename}>
        {file.filename}
      </span>
      <span className="fmeta">{humanSize(file.size_bytes)}</span>
      <span className={`scanpill ${infected ? "infected" : "clean"}`}>
        {file.scan_status}
      </span>
      <span className="fmeta">{shortDate(file.created_at)}</span>
      <span className="facts">
        {confirming ? (
          <>
            <button
              className="iconbtn danger"
              aria-label={`confirm delete ${file.filename}`}
              onClick={onDelete}
            >
              <Check size={16} />
            </button>
            <button
              className="iconbtn"
              aria-label="cancel delete"
              onClick={onCancelDelete}
            >
              <X size={16} />
            </button>
          </>
        ) : (
          <>
            {!infected && (
              <a
                className="iconbtn"
                aria-label={`download ${file.filename}`}
                href={`${apiBase()}/api/v1/files/${file.id}/download`}
              >
                <Download size={16} />
              </a>
            )}
            <button
              className="iconbtn danger"
              aria-label={`delete ${file.filename}`}
              onClick={onAskDelete}
            >
              <Trash2 size={16} />
            </button>
          </>
        )}
      </span>
    </div>
  );
}

export default function FilesPage() {
  const {
    files,
    page,
    status,
    error,
    hasNextPage,
    uploads,
    notice,
    fetchPage,
    deleteFile,
    upload,
    dismissUpload,
    setNotice,
  } = useFilesStore();
  const [destination, setDestination] = useState<UploadDestination>("library");
  const [dragging, setDragging] = useState(false);
  const [confirmingId, setConfirmingId] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (status === "idle") void fetchPage(1);
  }, [status, fetchPage]);

  const takeFiles = useCallback(
    (list: FileList | null) => {
      if (!list || list.length === 0) return;
      if (list.length > 1)
        setNotice("one file at a time — uploading the first only");
      const file = list[0];
      const problem = validateFile(file, destination);
      if (problem) {
        setNotice(problem);
        return;
      }
      void upload(file, destination);
    },
    [destination, upload, setNotice],
  );

  return (
    <>
      <VaporwaveScene />
      <div className="arenahead">
        <div className="name">
          <Folder size={17} />
          Files
        </div>
        <span className="kicker">{`// page ${page}`}</span>
      </div>
      <div className="banner">
        every byte is scanned before it touches the shelf
      </div>

      <div className="fwrap">
        {notice && (
          <div className="fnotice" role="status">
            <span>{notice}</span>
            <button
              className="iconbtn"
              aria-label="dismiss notice"
              onClick={() => setNotice(null)}
            >
              <X size={15} />
            </button>
          </div>
        )}

        <div
          className={`dropzone${dragging ? " drag" : ""}`}
          data-testid="files-dropzone"
          onClick={() => inputRef.current?.click()}
          onDragOver={(e) => {
            e.preventDefault();
            setDragging(true);
          }}
          onDragLeave={() => setDragging(false)}
          onDrop={(e) => {
            e.preventDefault();
            setDragging(false);
            takeFiles(e.dataTransfer.files);
          }}
        >
          <UploadCloud size={26} />
          <div>
            drop a file here or <b>click to browse</b>
          </div>
          <span className="dz-hint">
            {destination === "bookdrop"
              ? "pdf / epub → bookdrop, max 100 MiB"
              : "pdf, epub, images, audio, video — max 100 MiB"}
          </span>
          <input
            ref={inputRef}
            type="file"
            hidden
            data-testid="files-input"
            onChange={(e) => {
              takeFiles(e.target.files);
              e.target.value = "";
            }}
          />
        </div>

        <div className="destrow">
          <span className="kicker">{"// destination"}</span>
          <button
            className={`chip${destination === "library" ? " on" : ""}`}
            onClick={() => setDestination("library")}
          >
            <Folder size={13} /> library
          </button>
          <button
            className={`chip${destination === "bookdrop" ? " on" : ""}`}
            onClick={() => setDestination("bookdrop")}
          >
            <BookOpen size={13} /> bookdrop
          </button>
        </div>

        {uploads.map((u) => (
          <div
            key={u.key}
            className={`uprow${u.state.phase === "error" ? " err" : ""}`}
          >
            <span className="upname">{u.filename}</span>
            {u.state.phase === "uploading" && (
              <>
                <span className="upbar">
                  <b style={{ width: `${u.state.percent}%` }} />
                </span>
                <span className="upstate">{u.state.percent}%</span>
              </>
            )}
            {u.state.phase === "scanning" && (
              <>
                <span className="upbar scan">
                  <b />
                </span>
                <span className="upstate">scanning…</span>
              </>
            )}
            {u.state.phase === "error" && (
              <>
                <span className="upstate">{u.state.message}</span>
                <button
                  className="iconbtn"
                  aria-label="dismiss upload"
                  onClick={() => dismissUpload(u.key)}
                >
                  <X size={15} />
                </button>
              </>
            )}
          </div>
        ))}

        {status === "error" && (
          <div className="placeholder">
            <h2>Shelf unreachable.</h2>
            <p>{error}</p>
            <button
              className="btn-ghost btn-sm"
              onClick={() => fetchPage(page)}
            >
              retry
            </button>
          </div>
        )}

        {status === "ready" && files.length === 0 && (
          <div className="placeholder" data-testid="files-empty">
            <h2>Nothing on the shelf.</h2>
            <p>drop something above — it gets scanned, hashed and kept.</p>
          </div>
        )}

        {files.length > 0 && (
          <div className="ftable" data-testid="files-table">
            <div className="frow head">
              <span />
              <span>name</span>
              <span>size</span>
              <span>scan</span>
              <span>added</span>
              <span style={{ textAlign: "right" }}>actions</span>
            </div>
            {files.map((f) => (
              <FileRow
                key={f.id}
                file={f}
                confirming={confirmingId === f.id}
                onAskDelete={() => setConfirmingId(f.id)}
                onCancelDelete={() => setConfirmingId(null)}
                onDelete={() => {
                  setConfirmingId(null);
                  void deleteFile(f.id);
                }}
              />
            ))}
          </div>
        )}

        {(page > 1 || hasNextPage) && (
          <div className="fpager">
            <button
              className="iconbtn"
              aria-label="previous page"
              disabled={page <= 1 || status === "loading"}
              onClick={() => fetchPage(page - 1)}
            >
              <ChevronLeft size={17} />
            </button>
            <span className="pageno">page {page}</span>
            <button
              className="iconbtn"
              aria-label="next page"
              disabled={!hasNextPage || status === "loading"}
              onClick={() => fetchPage(page + 1)}
            >
              <ChevronRight size={17} />
            </button>
          </div>
        )}
      </div>
    </>
  );
}
