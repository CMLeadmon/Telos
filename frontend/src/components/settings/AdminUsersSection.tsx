"use client";

import { useCallback, useEffect, useState } from "react";
import { api, ApiError } from "@/lib/api";
import { useAuthStore } from "@/stores/useAuthStore";

interface AdminUser {
  id: string;
  username: string;
  displayName: string;
  active: boolean;
  createdAt: string;
  roles: string[];
  hasAvatar: boolean;
}

interface RoleInfo {
  id: string;
  name: string;
  builtin: boolean;
  memberCount: number;
  permissions: string[];
}

export function AdminUsersSection() {
  const me = useAuthStore((s) => s.user);
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [roles, setRoles] = useState<RoleInfo[]>([]);
  const [msg, setMsg] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);

  const load = useCallback(() => {
    Promise.all([
      api<AdminUser[]>("/api/v1/admin/users"),
      api<RoleInfo[]>("/api/v1/admin/roles"),
    ])
      .then(([u, r]) => {
        setUsers(u);
        setRoles(r);
      })
      .catch((err) => {
        setMsg(err instanceof ApiError ? err.message : "Failed to load members.");
      });
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const run = async (fn: () => Promise<unknown>) => {
    setMsg(null);
    try {
      await fn();
      load();
    } catch (err) {
      setMsg(err instanceof ApiError ? err.message : "Action failed.");
    }
  };

  const toggleRole = (u: AdminUser, roleId: string) => {
    const next = u.roles.includes(roleId)
      ? u.roles.filter((r) => r !== roleId)
      : [...u.roles, roleId];
    void run(() =>
      api(`/api/v1/admin/users/${u.id}/roles`, {
        method: "PUT",
        body: JSON.stringify({ roles: next }),
      }),
    );
  };

  return (
    <>
      <h2>Members</h2>
      {msg && <span className="setmsg err">{msg}</span>}
      <div className="setcard">
        <table className="settable">
          <thead>
            <tr>
              <th>Member</th>
              <th>Roles</th>
              <th>Status</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {users.map((u) => (
              <tr key={u.id}>
                <td>
                  <b>{u.displayName || u.username}</b>{" "}
                  <span className="mono" style={{ opacity: 0.6 }}>
                    @{u.username}
                  </span>
                </td>
                <td>
                  <div style={{ display: "flex", flexWrap: "wrap", gap: 4 }}>
                    {roles.map((r) => (
                      <button
                        key={r.id}
                        className={`chip${u.roles.includes(r.id) ? " on" : ""}`}
                        disabled={u.id === me?.ID}
                        title={
                          u.id === me?.ID
                            ? "You cannot change your own roles"
                            : `Toggle ${r.name}`
                        }
                        onClick={() => toggleRole(u, r.id)}
                      >
                        {r.name}
                      </button>
                    ))}
                  </div>
                </td>
                <td>
                  {u.active ? (
                    <span className="tag">active</span>
                  ) : (
                    <span className="tag">disabled</span>
                  )}
                </td>
                <td>
                  {u.id !== me?.ID && (
                    <div style={{ display: "flex", gap: 6, justifyContent: "flex-end" }}>
                      <button
                        className="btn-ghost btn-sm"
                        onClick={() =>
                          void run(() =>
                            api(`/api/v1/admin/users/${u.id}/active`, {
                              method: "POST",
                              body: JSON.stringify({ active: !u.active }),
                            }),
                          )
                        }
                      >
                        {u.active ? "Disable" : "Enable"}
                      </button>
                      {confirmDelete === u.id ? (
                        <button
                          className="btn rose btn-sm"
                          onClick={() => {
                            setConfirmDelete(null);
                            void run(() =>
                              api(`/api/v1/admin/users/${u.id}`, { method: "DELETE" }),
                            );
                          }}
                        >
                          Confirm delete
                        </button>
                      ) : (
                        <button
                          className="btn-ghost btn-sm"
                          onClick={() => setConfirmDelete(u.id)}
                        >
                          Delete
                        </button>
                      )}
                    </div>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        <span className="setmsg" style={{ color: "var(--faint)" }}>
          Deleting a member anonymizes the account; their messages remain attributed
          to &ldquo;Deleted User&rdquo;.
        </span>
      </div>
    </>
  );
}
