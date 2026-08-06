import {
  BadgeInfo,
  Hash,
  KeyRound,
  Mail,
  Palette,
  Shield,
  User,
  Users,
  type LucideIcon,
} from "lucide-react";

// Section metadata only — no panel components. The module rail renders this
// list, and pulling the panels in with it would drag every settings screen
// into the shell's bundle. The page maps id -> component separately.
export interface SettingsSection {
  id: string;
  label: string;
  icon: LucideIcon;
  admin: boolean;
  capability?: string;
}

export const SETTINGS_SECTIONS: SettingsSection[] = [
  { id: "profile", label: "Profile", icon: User, admin: false },
  { id: "security", label: "Security", icon: Shield, admin: false },
  { id: "appearance", label: "Appearance", icon: Palette, admin: false },
  { id: "credits", label: "Credits", icon: BadgeInfo, admin: false },
  { id: "users", label: "Members", icon: Users, admin: true, capability: "manage_members" },
  { id: "invites", label: "Invites", icon: Mail, admin: true, capability: "create_invites" },
  { id: "roles", label: "Roles", icon: KeyRound, admin: true, capability: "manage_roles" },
  { id: "channels", label: "Channels", icon: Hash, admin: true, capability: "manage_channels" },
];
