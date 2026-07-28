"use client";

import { useEffect, useState } from "react";
import { api, ApiError } from "@/lib/api";

interface Channel {
  id: string;
  name: string;
}

interface RoleInfo {
  id: string;
  name: string;
}

type Decision = "inherit" | "allow" | "deny";

// Permissions that can be overridden per channel/role.
const OVERRIDABLE = ["view_channel", "send_messages"];
const DEFAULT_ROLES = ["Administrator", "Moderator", "Member"];

export function AdminChannelsSection() {
  const [channels, setChannels] = useState<Channel[]>([]);
  const [roles, setRoles] = useState<string[]>(DEFAULT_ROLES);
  const [error, setError] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [selected, setSelected] = useState<string | null>(null);
  const [overrides, setOverrides] = useState<Record<string, Record<string, Decision>>>({});

  const load = () =>
    api<Channel[]>("/api/v1/channels")
      .then((c) => setChannels(c))
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load channels."));

  useEffect(() => {
    // Promise-chain loader (setState only inside .then, never synchronously in
    // the effect body) satisfies the setState-in-effect lint rule.
    void load();
    api<RoleInfo[]>("/api/v1/admin/roles")
      .then((r) => {
        const nonOwner = r.map((role) => role.id).filter((id) => id !== "Owner");
        if (nonOwner.length > 0) setRoles(nonOwner);
      })
      .catch(() => {
        // Fall back to default non-Owner roles if endpoint fails
      });
  }, []);

  const create = async () => {
    setError(null);
    try {
      await api("/api/v1/admin/channels", {
        method: "POST",
        body: JSON.stringify({ name }),
      });
      setName("");
      await load();
    } catch (e) {
      // The draft name is preserved for retry.
      setError(e instanceof ApiError ? e.message : "Could not create the channel.");
    }
  };

  const remove = async (id: string) => {
    if (!window.confirm("Delete this channel? This cannot be undone.")) return;
    setError(null);
    try {
      await api(`/api/v1/admin/channels/${id}`, { method: "DELETE" });
      await load();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not delete the channel.");
    }
  };

  const openOverrides = async (id: string) => {
    setSelected(id);
    try {
      const res = await api<{ overrides: Record<string, Record<string, Decision>> }>(
        `/api/v1/admin/channels/${id}/overrides`,
      );
      setOverrides(res.overrides ?? {});
    } catch {
      setOverrides({});
    }
  };

  const setOverride = async (role: string, perm: string, decision: Decision) => {
    if (!selected) return;
    setOverrides((o) => ({ ...o, [role]: { ...(o[role] ?? {}), [perm]: decision } }));
    try {
      await api(`/api/v1/admin/channels/${selected}/overrides`, {
        method: "PUT",
        body: JSON.stringify({ roleId: role, permissionId: perm, decision }),
      });
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not update the override.");
      await openOverrides(selected);
    }
  };

  return (
    <div className="admin-channels" data-testid="admin-channels-section">
      <h2>Channels</h2>
      {error && (
        <p className="settings-error" role="alert" data-testid="admin-channels-error">
          {error}
        </p>
      )}

      <div className="channel-create">
        <input
          aria-label="Channel name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="channel-name"
        />
        <button onClick={() => void create()} data-testid="create-channel">
          Create
        </button>
      </div>

      <ul className="channel-list">
        {channels.map((c) => (
          <li key={c.id} data-testid={`channel-${c.id}`}>
            <span className="channel-name">#{c.name}</span>
            <button onClick={() => void openOverrides(c.id)}>Overrides</button>
            <button
              onClick={() => void remove(c.id)}
              data-testid={`delete-channel-${c.id}`}
            >
              Delete
            </button>
          </li>
        ))}
      </ul>

      {selected && (
        <div className="override-editor" data-testid="override-editor">
          <h3>Role overrides</h3>
          <table>
            <thead>
              <tr>
                <th>Role</th>
                {OVERRIDABLE.map((p) => (
                  <th key={p}>{p}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {roles.map((role) => (
                <tr key={role}>
                  <td>{role}</td>
                  {OVERRIDABLE.map((perm) => (
                    <td key={perm}>
                      <select
                        aria-label={`${role} ${perm}`}
                        value={overrides[role]?.[perm] ?? "inherit"}
                        onChange={(e) =>
                          void setOverride(role, perm, e.target.value as Decision)
                        }
                      >
                        <option value="inherit">inherit</option>
                        <option value="allow">allow</option>
                        <option value="deny">deny</option>
                      </select>
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
