"use client";

import { useCallback, useEffect, useState } from "react";
import { api, ApiError } from "@/lib/api";

interface Invite {
  id: string;
  creator: string;
  roleId: string;
  createdAt: string;
  expiresAt: string;
}

interface RoleInfo {
  id: string;
  name: string;
}

export function AdminInvitesSection() {
  const [invites, setInvites] = useState<Invite[]>([]);
  const [roles, setRoles] = useState<RoleInfo[]>([]);
  const [roleId, setRoleId] = useState("Member");
  const [newToken, setNewToken] = useState<string | null>(null);
  const [msg, setMsg] = useState<string | null>(null);

  const load = useCallback(() => {
    Promise.all([
      api<Invite[]>("/api/v1/admin/invites"),
      api<RoleInfo[]>("/api/v1/admin/roles"),
    ])
      .then(([inv, r]) => {
        setInvites(inv);
        setRoles(r);
      })
      .catch((err) => {
        setMsg(err instanceof ApiError ? err.message : "Failed to load invites.");
      });
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const createInvite = async () => {
    setMsg(null);
    try {
      const res = await api<{ invite_token: string }>("/api/v1/auth/invites", {
        method: "POST",
        body: JSON.stringify({ roleId }),
      });
      setNewToken(res.invite_token);
      load();
    } catch (err) {
      setMsg(err instanceof ApiError ? err.message : "Invite creation failed.");
    }
  };

  const revoke = async (id: string) => {
    await api(`/api/v1/admin/invites/${id}`, { method: "DELETE" });
    load();
  };

  return (
    <>
      <h2>Invites</h2>
      <div className="setcard">
        <div className="setrow">
          <div className="lbl">
            <b>Create invite</b>
            <span>
              Single-use, valid for 7 days. The new member joins with the selected
              role.
            </span>
          </div>
          <div style={{ display: "flex", gap: 8 }}>
            <select
              value={roleId}
              onChange={(e) => setRoleId(e.target.value)}
              aria-label="invite role"
            >
              {roles.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.name}
                </option>
              ))}
            </select>
            <button className="btn btn-sm" onClick={() => void createInvite()}>
              Generate
            </button>
          </div>
        </div>
        {newToken && (
          <div className="setrow">
            <input
              className="mono"
              readOnly
              value={newToken}
              style={{ flex: 1 }}
              onFocus={(e) => e.target.select()}
            />
            <button
              className="btn-ghost btn-sm"
              onClick={() => void navigator.clipboard.writeText(newToken)}
            >
              Copy
            </button>
          </div>
        )}
        {msg && <span className="setmsg err">{msg}</span>}
      </div>

      <div className="setcard">
        <b>Pending invites</b>
        <table className="settable">
          <thead>
            <tr>
              <th>Created by</th>
              <th>Role</th>
              <th>Expires</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {invites.map((i) => (
              <tr key={i.id}>
                <td>{i.creator}</td>
                <td>
                  <span className="tag">{i.roleId}</span>
                </td>
                <td>{new Date(i.expiresAt).toLocaleString()}</td>
                <td>
                  <button className="btn-ghost btn-sm" onClick={() => void revoke(i.id)}>
                    Revoke
                  </button>
                </td>
              </tr>
            ))}
            {invites.length === 0 && (
              <tr>
                <td colSpan={4} style={{ color: "var(--faint)" }}>
                  No pending invites.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </>
  );
}
