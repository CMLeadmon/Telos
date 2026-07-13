"use client";

import { useState } from "react";
import { KeyRound, Mail, Mic, Palette, Shield, User, Users } from "lucide-react";
import { useAuthStore } from "@/stores/useAuthStore";
import { ProfileSection } from "@/components/settings/ProfileSection";
import { SecuritySection } from "@/components/settings/SecuritySection";
import { AppearanceSection } from "@/components/settings/AppearanceSection";
import { VoiceAudioSection } from "@/components/settings/VoiceAudioSection";
import { AdminUsersSection } from "@/components/settings/AdminUsersSection";
import { AdminInvitesSection } from "@/components/settings/AdminInvitesSection";
import { AdminRolesSection } from "@/components/settings/AdminRolesSection";

const SECTIONS = [
  { id: "profile", label: "Profile", icon: User, admin: false, C: ProfileSection },
  { id: "security", label: "Security", icon: Shield, admin: false, C: SecuritySection },
  { id: "appearance", label: "Appearance", icon: Palette, admin: false, C: AppearanceSection },
  { id: "voice", label: "Voice & Audio", icon: Mic, admin: false, C: VoiceAudioSection },
  { id: "users", label: "Members", icon: Users, admin: true, C: AdminUsersSection },
  { id: "invites", label: "Invites", icon: Mail, admin: true, C: AdminInvitesSection },
  { id: "roles", label: "Roles", icon: KeyRound, admin: true, C: AdminRolesSection },
] as const;

export default function SettingsPage() {
  const user = useAuthStore((s) => s.user);
  const [active, setActive] = useState<string>("profile");
  // Client-side visibility only — every admin endpoint enforces permissions server-side.
  const isAdmin =
    user?.Roles.some((r) => r === "Owner" || r === "Administrator") ?? false;
  const visible = SECTIONS.filter((s) => !s.admin || isAdmin);
  const current = visible.find((s) => s.id === active) ?? visible[0];
  const Panel = current.C;

  return (
    <div className="settings" data-testid="settings-page">
      <nav className="setnav">
        <h3 className="grouplabel">User settings</h3>
        {visible
          .filter((s) => !s.admin)
          .map(({ id, label, icon: Icon }) => (
            <button
              key={id}
              className={`setnavbtn${current.id === id ? " on" : ""}`}
              onClick={() => setActive(id)}
            >
              <Icon size={16} /> {label}
            </button>
          ))}
        {isAdmin && (
          <>
            <h3 className="grouplabel">Administration</h3>
            {visible
              .filter((s) => s.admin)
              .map(({ id, label, icon: Icon }) => (
                <button
                  key={id}
                  className={`setnavbtn${current.id === id ? " on" : ""}`}
                  onClick={() => setActive(id)}
                >
                  <Icon size={16} /> {label}
                </button>
              ))}
          </>
        )}
      </nav>
      <div className="setpanel">
        <Panel />
      </div>
    </div>
  );
}
