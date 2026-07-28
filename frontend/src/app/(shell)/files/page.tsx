"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";

// Files folded into the Library module. This route stays only to keep old
// links, bookmarks, and shared chat cards working — it forwards to the Files
// segment of the Library.
export default function FilesRedirect() {
  const router = useRouter();
  useEffect(() => {
    router.replace("/library?view=files");
  }, [router]);
  return (
    <div className="authwrap">
      <span className="kicker">{"// opening files…"}</span>
    </div>
  );
}
