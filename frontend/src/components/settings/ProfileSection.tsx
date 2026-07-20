"use client";

import { useRef, useState } from "react";
import { api, apiBase, avatarUrl, ApiError } from "@/lib/api";
import { useAuthStore } from "@/stores/useAuthStore";

export function ProfileSection() {
  const { user, fetchMe } = useAuthStore();
  const [displayName, setDisplayName] = useState(user?.DisplayName ?? "");
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);
  const [cacheBust, setCacheBust] = useState(0);

  const saveName = async () => {
    setBusy(true);
    setMsg(null);
    try {
      await api("/api/v1/users/me", {
        method: "PATCH",
        body: JSON.stringify({ displayName }),
      });
      await fetchMe();
      setMsg({ ok: true, text: "Profile saved." });
    } catch (err) {
      setMsg({
        ok: false,
        text: err instanceof ApiError ? err.message : "Save failed.",
      });
    } finally {
      setBusy(false);
    }
  };

  const uploadAvatar = async (file: File) => {
    setBusy(true);
    setMsg(null);
    try {
      const form = new FormData();
      form.append("file", file);
      // Multipart request — the browser sets the Content-Type boundary itself.
      const res = await fetch(`${apiBase()}/api/v1/users/me/avatar`, {
        method: "POST",
        credentials: "include",
        body: form,
      });
      if (!res.ok) throw new ApiError(res.status, (await res.text()).trim());
      await fetchMe();
      setCacheBust((n) => n + 1);
      setMsg({ ok: true, text: "Avatar updated (scanned clean)." });
    } catch (err) {
      setMsg({
        ok: false,
        text: err instanceof ApiError ? err.message : "Upload failed.",
      });
    } finally {
      setBusy(false);
    }
  };

  const removeAvatar = async () => {
    await api("/api/v1/users/me/avatar", { method: "DELETE" });
    await fetchMe();
    setCacheBust((n) => n + 1);
  };

  if (!user) return null;
  return (
    <>
      <h2>Profile</h2>
      <div className="setcard">
        <div className="setrow">
          {user.HasAvatar ? (
            // eslint-disable-next-line @next/next/no-img-element
            <img
              className="setava"
              src={`${avatarUrl(user.ID)}?v=${cacheBust}`}
              alt="Your avatar"
            />
          ) : (
            <div className="setava" style={{ display: "grid", placeItems: "center" }}>
              {user.Username.slice(0, 2).toUpperCase()}
            </div>
          )}
          <div style={{ display: "flex", gap: 8 }}>
            <button
              className="btn-ghost btn-sm"
              disabled={busy}
              onClick={() => fileRef.current?.click()}
            >
              Upload avatar
            </button>
            {user.HasAvatar && (
              <button
                className="btn-ghost btn-sm"
                disabled={busy}
                onClick={() => void removeAvatar()}
              >
                Remove
              </button>
            )}
          </div>
          <input
            ref={fileRef}
            type="file"
            accept=".jpg,.jpeg,.png,.webp"
            hidden
            onChange={(e) => {
              const f = e.target.files?.[0];
              if (f) void uploadAvatar(f);
              e.target.value = "";
            }}
          />
        </div>
        <div className="setfield">
          <label htmlFor="set-displayname">Display name</label>
          <input
            id="set-displayname"
            value={displayName}
            maxLength={64}
            placeholder={user.Username}
            onChange={(e) => setDisplayName(e.target.value)}
          />
        </div>
        <div className="setfield">
          <label htmlFor="set-username">Username</label>
          <input id="set-username" value={user.Username} disabled />
        </div>
        <div className="setrow">
          <button
            className="btn cyan btn-sm"
            disabled={busy}
            onClick={() => void saveName()}
          >
            Save profile
          </button>
          {msg && (
            <span className={`setmsg ${msg.ok ? "ok" : "err"}`}>{msg.text}</span>
          )}
        </div>
      </div>
    </>
  );
}
