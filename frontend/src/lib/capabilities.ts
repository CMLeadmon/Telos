import type { CurrentUser } from "@/stores/useAuthStore";

/**
 * Evaluates whether a user has a specific capability.
 * The Owner role inherently holds all capabilities.
 * Otherwise, the permission must be present in the user's Permissions array.
 */
export function hasCapability(
  user: CurrentUser | null | undefined,
  capability: string,
): boolean {
  if (!user) return false;
  if (user.Roles?.includes("Owner")) return true;
  return user.Permissions?.includes(capability) ?? false;
}

/**
 * Evaluates whether a user can access any administration settings section.
 */
export function canAccessAdmin(user: CurrentUser | null | undefined): boolean {
  return (
    hasCapability(user, "manage_members") ||
    hasCapability(user, "create_invites") ||
    hasCapability(user, "manage_roles") ||
    hasCapability(user, "manage_channels")
  );
}
