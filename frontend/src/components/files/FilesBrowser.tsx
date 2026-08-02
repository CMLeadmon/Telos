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
  FolderPlus,
  Image as ImageIcon,
  MessageSquare,
  Music,
  Share2,
  Trash2,
  UploadCloud,
  X,
} from "lucide-react";
import { apiBase } from "@/lib/api";
import {
  type FileEntry,
  type FolderEntry,
  type UploadDestination,
  useFilesStore,
  validateFile,
} from "@/stores/useFilesStore";
import { useAuthStore } from "@/stores/useAuthStore";
import { hasCapability } from "@/lib/capabilities";
import { CommentaryPanel } from "@/components/CommentaryPanel";

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

function FolderRow({
  folder,
  confirming,
  onEnter,
  onAskDelete,
  onCancelDelete,
  onDelete,
}: {
  folder: FolderEntry;
  confirming: boolean;
  onEnter: () => void;
  onAskDelete: () => void;
  onCancelDelete: () => void;
  onDelete: () => void;
}) {
  return (
    <div className="frow folder" data-testid="folder-row" onClick={onEnter}>
      <span className="ficon">
        <Folder size={17} />
      </span>
      <span className="fname" title={folder.name}>
        {folder.name}
      </span>
      <span className="fmeta">—</span>
      <span className="fmeta" />
      <span className="fmeta">folder</span>
      <span className="facts" onClick={(e) => e.stopPropagation()}>
        {confirming ? (
          <>
            <button
              className="iconbtn danger"
              aria-label={`confirm delete ${folder.name}`}
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
          <button
            className="iconbtn danger"
            aria-label={`delete folder ${folder.name}`}
            onClick={onAskDelete}
          >
            <Trash2 size={16} />
          </button>
        )}
      </span>
    </div>
  );
}

function FileRow({
  file,
  confirming,
  commentsOpen,
  onToggleComments,
  onAskDelete,
  onCancelDelete,
  onDelete,
}: {
  file: FileEntry;
  confirming: boolean;
  commentsOpen: boolean;
  onToggleComments: () => void;
  onAskDelete: () => void;
  onCancelDelete: () => void;
  onDelete: () => void;
}) {
  const infected = file.scan_status !== "clean";
  return (
    <div className={`frow${infected ? " infected" : ""}`} data-testid="file-row">
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
              <>
                <a
                  className="iconbtn"
                  aria-label={`share ${file.filename} to chat`}
                  title="Share to chat"
                  href={`/chat?share_kind=file&share_ref=${encodeURIComponent(file.id)}`}
                >
                  <Share2 size={16} />
                </a>
                <a
                  className="iconbtn"
                  aria-label={`download ${file.filename}`}
                  href={`${apiBase()}/api/v1/files/${file.id}/download`}
                >
                  <Download size={16} />
                </a>
              </>
            )}
            <button
              className={`iconbtn${commentsOpen ? " on" : ""}`}
              aria-label={`comments on ${file.filename}`}
              aria-pressed={commentsOpen}
              data-testid={`file-comments-${file.id}`}
              onClick={onToggleComments}
            >
              <MessageSquare size={16} />
            </button>
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

function Breadcrumbs({
  path,
  onNavigate,
}: {
  path: string;
  onNavigate: (path: string) => void;
}) {
  const segments = path ? path.split("/") : [];
  return (
    <div className="crumbs" data-testid="files-breadcrumbs">
      <button
        className={`crumb${segments.length === 0 ? " here" : ""}`}
        onClick={() => onNavigate("")}
      >
        Home
      </button>
      {segments.map((seg, i) => {
        const target = segments.slice(0, i + 1).join("/");
        return (
          <span key={target} style={{ display: "contents" }}>
            <span className="sep">/</span>
            <button
              className={`crumb${i === segments.length - 1 ? " here" : ""}`}
              onClick={() => onNavigate(target)}
            >
              {seg}
            </button>
          </span>
        );
      })}
    </div>
  );
}

export function FilesBrowser() {
  const {
    path,
    folders,
    files,
    page,
    status,
    error,
    hasNextPage,
    uploads,
    notice,
    fetchDir,
    fetchPage,
    enterFolder,
    navigateTo,
    createFolder,
    deleteFolder,
    deleteFile,
    upload,
    dismissUpload,
    setNotice,
  } = useFilesStore();
  const [destination, setDestination] = useState<UploadDestination>("library");
  const [dragging, setDragging] = useState(false);
  const [confirmingId, setConfirmingId] = useState<string | null>(null);
  const [creatingFolder, setCreatingFolder] = useState(false);
  const [folderName, setFolderName] = useState("");
  const [commentsFor, setCommentsFor] = useState<FileEntry | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const canModerateComments = useAuthStore((s) =>
    hasCapability(s.user, "moderate_annotations"),
  );

  useEffect(() => {
    if (status === "idle") void fetchDir("", 1);
  }, [status, fetchDir]);

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

  const submitFolder = () => {
    const name = folderName.trim();
    if (name) void createFolder(name);
    setFolderName("");
    setCreatingFolder(false);
  };

  const hasRows = folders.length > 0 || files.length > 0;
  const uploadTarget = path ? `/${path}` : "root";

  return (
    <>
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
        <div className="destrow" style={{ justifyContent: "space-between" }}>
          <Breadcrumbs path={path} onNavigate={(p) => void navigateTo(p)} />
          {creatingFolder ? (
            <div className="newfolderbar">
              <input
                autoFocus
                data-testid="new-folder-input"
                placeholder="folder name"
                value={folderName}
                onChange={(e) => setFolderName(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") submitFolder();
                  if (e.key === "Escape") {
                    setFolderName("");
                    setCreatingFolder(false);
                  }
                }}
              />
              <button
                className="iconbtn"
                aria-label="confirm new folder"
                onClick={submitFolder}
              >
                <Check size={16} />
              </button>
            </div>
          ) : (
            <button
              className="btn-ghost btn-sm"
              data-testid="new-folder-btn"
              onClick={() => setCreatingFolder(true)}
            >
              <FolderPlus size={15} /> New folder
            </button>
          )}
        </div>

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
              ? "pdf / epub → bookdrop (flat), max 100 MiB"
              : `uploading to ${uploadTarget} — max 100 MiB`}
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

        {status === "ready" && !hasRows && (
          <div className="placeholder" data-testid="files-empty">
            <h2>{path ? "Empty folder." : "Nothing on the shelf."}</h2>
            <p>drop something above, or make a folder to organize.</p>
          </div>
        )}

        {hasRows && (
          <div className="ftable" data-testid="files-table">
            <div className="frow head">
              <span />
              <span>name</span>
              <span>size</span>
              <span>scan</span>
              <span>added</span>
              <span style={{ textAlign: "right" }}>actions</span>
            </div>
            {folders.map((f) => (
              <FolderRow
                key={f.id}
                folder={f}
                confirming={confirmingId === f.id}
                onEnter={() => {
                  setConfirmingId(null);
                  void enterFolder(f.path);
                }}
                onAskDelete={() => setConfirmingId(f.id)}
                onCancelDelete={() => setConfirmingId(null)}
                onDelete={() => {
                  setConfirmingId(null);
                  void deleteFolder(f.id);
                }}
              />
            ))}
            {files.map((f) => (
              <FileRow
                key={f.id}
                file={f}
                confirming={confirmingId === f.id}
                commentsOpen={commentsFor?.id === f.id}
                onToggleComments={() =>
                  setCommentsFor((cur) => (cur?.id === f.id ? null : f))
                }
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

        {commentsFor && (
          <div className="file-commentary" data-testid="file-commentary">
            <div className="file-commentary-head">
              <span className="fname">{commentsFor.filename}</span>
              <button
                className="iconbtn"
                aria-label="close comments"
                onClick={() => setCommentsFor(null)}
              >
                <X size={16} />
              </button>
            </div>
            <CommentaryPanel
              targetType="file"
              targetId={commentsFor.id}
              canModerate={canModerateComments}
            />
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
