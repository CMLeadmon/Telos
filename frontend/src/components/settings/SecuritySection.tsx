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

// A registered device is not a session. A session is a browser sign-in that
// expires in hours; a device holds a 90-day refresh token that survives every
// sign-out, so it needs its own list — signing out everywhere else does not
// touch one.
interface DeviceInfo {
  id: string;
  deviceName: string;
  platform: string;
  clientVersion: string;
  createdAt: string;
  lastSeenAt: string;
}

export function SecuritySection() {
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [sessions, setSessions] = useState<SessionInfo[]>([]);
  const [devices, setDevices] = useState<DeviceInfo[]>([]);
  const [deviceError, setDeviceError] = useState<string | null>(null);

  const loadSessions = useCallback(() => {
    api<SessionInfo[]>("/api/v1/users/me/sessions")
      .then((s) => setSessions(s))
      .catch(() => setSessions([]));
  }, []);

  const loadDevices = useCallback(() => {
    api<DeviceInfo[]>("/api/v1/users/me/devices")
      .then((d) => {
        setDevices(d ?? []);
        setDeviceError(null);
      })
      .catch((err) => {
        // An empty list and an unreachable node look identical on screen
        // otherwise, and the difference matters: one means nothing is
        // registered, the other means a device may still be out there.
        setDevices([]);
        setDeviceError(
          err instanceof ApiError ? err.message : "Could not load your devices.",
        );
      });
  }, []);

  useEffect(() => {
    loadSessions();
    loadDevices();
  }, [loadSessions, loadDevices]);

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

  const revokeDevice = async (id: string) => {
    setDeviceError(null);
    try {
      await api(`/api/v1/users/me/devices/${id}`, { method: "DELETE" });
    } catch (err) {
      // Reloading on a failed revoke would put the device back on screen with
      // no explanation, reading as though the click did nothing.
      setDeviceError(
        err instanceof ApiError ? err.message : "Could not revoke that device.",
      );
      return;
    }
    void loadDevices();
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

      <div className="setcard">
        <div className="setrow">
          <div className="lbl">
            <b>Registered devices</b>
            <span>
              Apps you installed and signed in from. Revoking one signs it out
              immediately and it will need your password again.
            </span>
          </div>
        </div>
        {deviceError && <span className="setmsg err">{deviceError}</span>}
        {!deviceError && devices.length === 0 && (
          <span className="setmsg">No devices are registered.</span>
        )}
        {devices.length > 0 && (
          <table className="settable">
            <thead>
              <tr>
                <th>Device</th>
                <th>Platform</th>
                <th>Last seen</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {devices.map((d) => (
                <tr key={d.id}>
                  <td>
                    {d.deviceName || "Unnamed device"}{" "}
                    <span className="tag">{d.clientVersion}</span>
                  </td>
                  <td>{d.platform || "—"}</td>
                  <td>{new Date(d.lastSeenAt ?? d.createdAt).toLocaleString()}</td>
                  <td>
                    <button
                      className="btn-ghost btn-sm"
                      onClick={() => void revokeDevice(d.id)}
                    >
                      Revoke
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </>
  );
}
