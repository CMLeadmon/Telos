"use client";

import {
  BadgeInfo,
  Hash,
  KeyRound,
  Mail,
  Palette,
  Shield,
  User,
  Users,
} from "lucide-react";
import { useAuthStore } from "@/stores/useAuthStore";
import { useSettingsStore } from "@/stores/useSettingsStore";
import { hasCapability } from "@/lib/capabilities";
import { ProfileSection } from "@/components/settings/ProfileSection";
import { SecuritySection } from "@/components/settings/SecuritySection";
import { AppearanceSection } from "@/components/settings/AppearanceSection";
import { AdminUsersSection } from "@/components/settings/AdminUsersSection";
import { AdminInvitesSection } from "@/components/settings/AdminInvitesSection";
import { AdminRolesSection } from "@/components/settings/AdminRolesSection";
import { AdminChannelsSection } from "@/components/settings/AdminChannelsSection";
import { CreditsSection } from "@/components/settings/CreditsSection";

const SECTIONS = [
  { id: "profile", label: "Profile", icon: User, admin: false, C: ProfileSection },
  { id: "security", label: "Security", icon: Shield, admin: false, C: SecuritySection },
  { id: "appearance", label: "Appearance", icon: Palette, admin: false, C: AppearanceSection },
  { id: "credits", label: "Credits", icon: BadgeInfo, admin: false, C: CreditsSection },
  { id: "users", label: "Members", icon: Users, admin: true, capability: "manage_members", C: AdminUsersSection },
  { id: "invites", label: "Invites", icon: Mail, admin: true, capability: "create_invites", C: AdminInvitesSection },
  { id: "roles", label: "Roles", icon: KeyRound, admin: true, capability: "manage_roles", C: AdminRolesSection },
  { id: "channels", label: "Channels", icon: Hash, admin: true, capability: "manage_channels", C: AdminChannelsSection },
] as const;

export default function SettingsPage() {
  const user = useAuthStore((s) => s.user);
  // Section choice lives in the store because the module rail renders the
  // section list, the same way every other module's sub-nav sits in the rail.
  const active = useSettingsStore((s) => s.activeSection);
  const setSection = useSettingsStore((s) => s.setSection);
  // Client-side visibility only — every admin endpoint enforces permissions server-side.
  const visible = SECTIONS.filter(
    (s) => !s.admin || (s.capability && hasCapability(user, s.capability)),
  );
  const current = visible.find((s) => s.id === active) ?? visible[0];
  const Panel = current.C;

  return (
    <div className="settings" data-testid="settings-page">
      {/* No arenahead here: each section renders its own <h2>, and several are
          more specific than the rail label ("Roles & permissions" vs "Roles"). */}
      {/* The module rail carries this list from 900px up. Below that the rail is
          gone, so the sections must stay reachable from the page itself. */}
      <nav className="setsections" aria-label="Settings sections">
        {visible.map(({ id, label, icon: Icon }) => (
          <button
            key={id}
            type="button"
            className={`setnavbtn${current.id === id ? " on" : ""}`}
            aria-current={current.id === id ? "page" : undefined}
            onClick={() => setSection(id)}
          >
            <Icon size={15} />
            {label}
          </button>
        ))}
      </nav>
      <div className="setpanel">
        <Panel />
      </div>
    </div>
  );
}

