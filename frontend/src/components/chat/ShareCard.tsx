"use client";

import React, { useState } from "react";
import { BookOpen, Film, Download } from "lucide-react";
import { apiBase } from "@/lib/api";
import { openNodeResource } from "@/lib/platform";
import { useRouter } from "next/navigation";

interface EmbedSnapshot {
  title: string;
  subtitle: string;
  kicker: string;
  cover: string;
  duration?: string;
}

interface ShareCardProps {
  embed: {
    kind: "library_book" | "stream_film" | "file";
    ref: string;
    snapshot: unknown;
  };
}

export function ShareCard({ embed }: ShareCardProps) {
  const router = useRouter();
  const [coverBroken, setCoverBroken] = useState(false);
  const snap = embed.snapshot as EmbedSnapshot;

  if (!snap) return null;

  const handleAction = () => {
    if (embed.kind === "library_book") {
      router.push(`/library/?read=${encodeURIComponent(embed.ref)}`);
    } else if (embed.kind === "stream_film") {
      router.push(`/stream/?play=${encodeURIComponent(embed.ref)}`);
    } else if (embed.kind === "file") {
      void openNodeResource(
        `/api/v1/files/${encodeURIComponent(embed.ref)}/download`,
        snap.title ?? "download",
      );
    }
  };

  const coverSrc = snap.cover ? `${apiBase()}${snap.cover}` : "";

  return (
    <div
      className="share-card"
      style={{
        display: "flex",
        background: "var(--surface)",
        border: "1px solid var(--line)",
        borderRadius: "var(--r)",
        padding: "10px",
        gap: "12px",
        marginTop: "6px",
        maxWidth: "450px",
        position: "relative",
      }}
    >
      {/* Cover / Icon */}
      <div
        className="share-cover"
        style={{
          width: "56px",
          height: "80px",
          borderRadius: "var(--r-xs)",
          background: "var(--surface-2)",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          overflow: "hidden",
          flex: "none",
          border: "1px solid var(--line-2)",
        }}
      >
        {coverSrc && !coverBroken ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img
            src={coverSrc}
            alt=""
            style={{ width: "100%", height: "100%", objectFit: "cover" }}
            onError={() => setCoverBroken(true)}
          />
        ) : embed.kind === "library_book" ? (
          <BookOpen size={24} style={{ color: "var(--faint)" }} />
        ) : embed.kind === "stream_film" ? (
          <Film size={24} style={{ color: "var(--faint)" }} />
        ) : (
          <Download size={24} style={{ color: "var(--faint)" }} />
        )}
      </div>

      {/* Meta */}
      <div
        style={{
          display: "flex",
          flexDirection: "column",
          minWidth: 0,
          flex: 1,
          justifyContent: "space-between",
        }}
      >
        <div>
          <div
            style={{
              fontSize: "10px",
              fontWeight: 600,
              textTransform: "uppercase",
              letterSpacing: "0.06em",
              color: "var(--accent)",
              marginBottom: "2px",
            }}
          >
            {snap.kicker ||
              (embed.kind === "library_book"
                ? "Book"
                : embed.kind === "stream_film"
                  ? "Media"
                  : "File")}
          </div>
          <div
            style={{
              fontSize: "13px",
              fontWeight: 700,
              color: "var(--ink)",
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
            }}
          >
            {snap.title}
          </div>
          <div
            style={{
              fontSize: "11px",
              color: "var(--faint)",
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
              marginTop: "1px",
            }}
          >
            {snap.subtitle}
          </div>
        </div>

        {/* Action Button */}
        <div style={{ display: "flex", gap: "6px", marginTop: "8px" }}>
          <button
            onClick={handleAction}
            style={{
              padding: "4px 10px",
              background: "var(--surface-3)",
              border: "1px solid var(--line-2)",
              borderRadius: "var(--r-xs)",
              color: "var(--ink)",
              fontSize: "11px",
              fontWeight: 600,
              cursor: "pointer",
              display: "flex",
              alignItems: "center",
              gap: "4px",
            }}
            className="share-card-action-btn"
          >
            {embed.kind === "library_book" ? (
              <>
                <BookOpen size={11} />
                Read
              </>
            ) : embed.kind === "stream_film" ? (
              <>
                <Film size={11} />
                Stream
              </>
            ) : (
              <>
                <Download size={11} />
                Download
              </>
            )}
          </button>
        </div>
      </div>
    </div>
  );
}
