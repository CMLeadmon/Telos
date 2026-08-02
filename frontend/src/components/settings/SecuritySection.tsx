"use client";

import { useCallback, useEffect, useState } from "react";
import { api, ApiError } from "@/lib/api";

interface SessionInfo {
  id: string;
  createdAt: string;
  lastSeenAt: string | null;
  userAgent: string;
  clientIp: string;
  current: boolean;
}

export function SecuritySection() {
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [sessions, setSessions] = useState<SessionInfo[]>([]);

  const loadSessions = useCallback(() => {
    api<SessionInfo[]>("/api/v1/users/me/sessions")
      .then((s) => setSessions(s))
      .catch(() => setSessions([]));
  }, []);

  useEffect(() => {
    loadSessions();
  }, [loadSessions]);

  const changePassword = async () => {
    setMsg(null);
    if (newPassword !== confirm) {
      setMsg({ ok: false, text: "New passwords do not match." });
      return;
    }
    if (newPassword.length < 15) {
      setMsg({ ok: false, text: "Password must be at least 15 characters." });
      return;
    }
    try {
      await api("/api/v1/users/me/password", {
        method: "POST",
        body: JSON.stringify({ currentPassword, newPassword }),
      });
      setCurrentPassword("");
      setNewPassword("");
      setConfirm("");
      setMsg({ ok: true, text: "Password changed. Other sessions were signed out." });
      void loadSessions();
    } catch (err) {
      setMsg({
        ok: false,
        text: err instanceof ApiError ? err.message : "Change failed.",
      });
    }
  };

  const revoke = async (id: string) => {
    await api(`/api/v1/users/me/sessions/${id}`, { method: "DELETE" });
    void loadSessions();
  };

  const revokeOthers = async () => {
    await api("/api/v1/users/me/sessions/revoke-others", { method: "POST" });
    void loadSessions();
  };

  return (
    <>
      <h2>Security</h2>
      <div className="setcard">
        <div className="setfield">
          <label htmlFor="set-curpw">Current password</label>
          <input
            id="set-curpw"
            type="password"
            value={currentPassword}
            onChange={(e) => setCurrentPassword(e.target.value)}
            autoComplete="current-password"
          />
        </div>
        <div className="setfield">
          <label htmlFor="set-newpw">New password (15+ characters)</label>
          <input
            id="set-newpw"
            type="password"
            value={newPassword}
            onChange={(e) => setNewPassword(e.target.value)}
            autoComplete="new-password"
          />
        </div>
        <div className="setfield">
          <label htmlFor="set-confpw">Confirm new password</label>
          <input
            id="set-confpw"
            type="password"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            autoComplete="new-password"
          />
        </div>
        <div className="setrow">
          <button className="btn btn-sm" onClick={() => void changePassword()}>
            Change password
          </button>
          {msg && (
            <span className={`setmsg ${msg.ok ? "ok" : "err"}`}>{msg.text}</span>
          )}
        </div>
      </div>

      <div className="setcard">
        <div className="setrow">
          <div className="lbl">
            <b>Active sessions</b>
            <span>Devices currently signed in to your account.</span>
          </div>
          <button className="btn-ghost btn-sm" onClick={() => void revokeOthers()}>
            Sign out everywhere else
          </button>
        </div>
        <table className="settable">
          <thead>
            <tr>
              <th>Device</th>
              <th>IP</th>
              <th>Last seen</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {sessions.map((s) => (
              <tr key={s.id}>
                <td>
                  {s.userAgent || "Unknown device"}{" "}
                  {s.current && <span className="tag">this device</span>}
                </td>
                <td className="mono">{s.clientIp || "—"}</td>
                <td>{new Date(s.lastSeenAt ?? s.createdAt).toLocaleString()}</td>
                <td>
                  {!s.current && (
                    <button
                      className="btn-ghost btn-sm"
                      onClick={() => void revoke(s.id)}
                    >
                      Revoke
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}
