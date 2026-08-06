"use client";

import { Folder } from "lucide-react";
import { useAuthStore } from "@/stores/useAuthStore";
import { hasCapability } from "@/lib/capabilities";
import { FilesBrowser } from "@/components/files/FilesBrowser";

// Files is one of the four top-level surfaces (Chat, Stream, Library, Files),
// so it owns a route rather than living as a segment of the Library.
export default function FilesPage() {
  const canViewFiles = useAuthStore((state) =>
    hasCapability(state.user, "view_files"),
  );

  // FilesBrowser renders its own arenahead and banner, so this route only
  // gates access and hands off.
  if (!canViewFiles) {
    return (
      <>
        <div className="arenahead">
          <div className="name">
            <Folder size={17} />
            Files
          </div>
        </div>
        <div className="emptystate">
          <span className="kicker">{"// no access"}</span>
          <p>You do not have permission to browse files on this node.</p>
        </div>
      </>
    );
  }

  return <FilesBrowser />;
}
