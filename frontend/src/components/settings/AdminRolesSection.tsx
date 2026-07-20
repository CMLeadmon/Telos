"use client";

import { useCallback, useEffect, useState } from "react";
import { api, ApiError } from "@/lib/api";
import { useAuthStore } from "@/stores/useAuthStore";

interface RoleInfo {
  id: string;
  name: string;
  builtin: boolean;
  memberCount: number;
  permissions: string[];
}

interface PermInfo {
  id: string;
  description: string;
}

export function AdminRolesSection() {
  const isOwner = useAuthStore((s) => s.user?.Roles.includes("Owner") ?? false);
  const [roles, setRoles] = useState<RoleInfo[]>([]);
  const [perms, setPerms] = useState<PermInfo[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [draft, setDraft] = useState<string[]>([]);
  const [newName, setNewName] = useState("");
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);

  const load = useCallback(() => {
    Promise.all([
      api<RoleInfo[]>("/api/v1/admin/roles"),
      api<PermInfo[]>("/api/v1/admin/permissions"),
    ])
      .then(([r, p]) => {
        setRoles(r);
        setPerms(p);
      })
      .catch((err) => {
        setMsg({
          ok: false,
          text: err instanceof ApiError ? err.message : "Failed to load roles.",
        });
      });
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const role = roles.find((r) => r.id === selected) ?? null;
  const editable =
    role !== null && role.id !== "Owner" && (!role.builtin || isOwner);

  const select = (r: RoleInfo) => {
    setSelected(r.id);
    setDraft(r.permissions);
    setMsg(null);
  };

  const savePerms = async () => {
    if (!role) return;
    try {
      await api(`/api/v1/admin/roles/${role.id}/permissions`, {
        method: "PUT",
        body: JSON.stringify({ permissions: draft }),
      });
      setMsg({ ok: true, text: "Permissions saved." });
      load();
    } catch (err) {
      setMsg({
        ok: false,
        text: err instanceof ApiError ? err.message : "Save failed.",
      });
    }
  };

  const createRole = async () => {
    try {
      await api("/api/v1/admin/roles", {
        method: "POST",
        body: JSON.stringify({ name: newName, permissions: [] }),
      });
      setNewName("");
      load();
    } catch (err) {
      setMsg({
        ok: false,
        text: err instanceof ApiError ? err.message : "Create failed.",
      });
    }
  };

  const deleteRole = async () => {
    if (!role) return;
    try {
      await api(`/api/v1/admin/roles/${role.id}`, { method: "DELETE" });
      setSelected(null);
      load();
    } catch (err) {
      setMsg({
        ok: false,
        text: err instanceof ApiError ? err.message : "Delete failed.",
      });
    }
  };

  return (
    <>
      <h2>Roles &amp; permissions</h2>
      <div className="setcard">
        <div className="setrow">
          <div className="setfield" style={{ flex: 1 }}>
            <label htmlFor="set-newrole">New role name</label>
            <input
              id="set-newrole"
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              placeholder="e.g. Book Club"
            />
          </div>
          <button
            className="btn cyan btn-sm"
            disabled={newName.trim().length < 2}
            onClick={() => void createRole()}
          >
            Create role
          </button>
        </div>
        <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
          {roles.map((r) => (
            <button
              key={r.id}
              className={`chip${selected === r.id ? " on" : ""}`}
              onClick={() => select(r)}
            >
              {r.name} · {r.memberCount}
            </button>
          ))}
        </div>
      </div>

      {role && (
        <div className={`setcard${!role.builtin ? " setdanger" : ""}`}>
          <div className="setrow">
            <div className="lbl">
              <b>{role.name}</b>
              <span>
                {role.id === "Owner"
                  ? "The Owner role always has every permission and cannot be edited."
                  : role.builtin
                    ? isOwner
                      ? "Built-in role — Owner-editable, role not deletable."
                      : "Built-in role — only an Owner can edit permissions."
                    : "Custom role."}
              </span>
            </div>
            {!role.builtin && (
              <button className="btn-ghost btn-sm" onClick={() => void deleteRole()}>
                Delete role
              </button>
            )}
          </div>
          <div className="setperms">
            {perms.map((p) => (
              <label key={p.id} className="setperm">
                <input
                  type="checkbox"
                  disabled={!editable}
                  checked={role.id === "Owner" ? true : draft.includes(p.id)}
                  onChange={(e) =>
                    setDraft((d) =>
                      e.target.checked ? [...d, p.id] : d.filter((x) => x !== p.id),
                    )
                  }
                />
                <span>
                  <span className="mono">{p.id}</span>
                  <br />
                  <span className="desc">{p.description}</span>
                </span>
              </label>
            ))}
          </div>
          {editable && (
            <div className="setrow">
              <button className="btn cyan btn-sm" onClick={() => void savePerms()}>
                Save permissions
              </button>
              {msg && (
                <span className={`setmsg ${msg.ok ? "ok" : "err"}`}>{msg.text}</span>
              )}
            </div>
          )}
        </div>
      )}
    </>
  );
}
