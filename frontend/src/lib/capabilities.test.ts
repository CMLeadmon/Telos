import { describe, expect, it } from "vitest";
import { hasCapability, canAccessAdmin } from "./capabilities";
import type { CurrentUser } from "@/stores/useAuthStore";

describe("capabilities", () => {
  it("returns false for null or undefined user", () => {
    expect(hasCapability(null, "view_channel")).toBe(false);
    expect(hasCapability(undefined, "view_channel")).toBe(false);
    expect(canAccessAdmin(null)).toBe(false);
  });

  it("grants all capabilities to Owner role regardless of Permissions array", () => {
    const owner: CurrentUser = {
      ID: "u-owner",
      Username: "owner",
      DisplayName: "Owner User",
      Roles: ["Owner"],
      Permissions: [],
      HasAvatar: false,
    };
    expect(hasCapability(owner, "view_channel")).toBe(true);
    expect(hasCapability(owner, "manage_members")).toBe(true);
    expect(hasCapability(owner, "arbitrary_capability")).toBe(true);
    expect(canAccessAdmin(owner)).toBe(true);
  });

  it("evaluates custom role capabilities based strictly on Permissions", () => {
    const customUser: CurrentUser = {
      ID: "u-custom",
      Username: "custom",
      DisplayName: "Custom User",
      Roles: ["CustomRole"],
      Permissions: ["view_channel", "send_messages", "manage_files"],
      HasAvatar: false,
    };

    expect(hasCapability(customUser, "view_channel")).toBe(true);
    expect(hasCapability(customUser, "send_messages")).toBe(true);
    expect(hasCapability(customUser, "manage_files")).toBe(true);
    expect(hasCapability(customUser, "manage_members")).toBe(false);
    expect(hasCapability(customUser, "manage_roles")).toBe(false);
    expect(canAccessAdmin(customUser)).toBe(false);
  });

  it("recognizes admin capabilities for users with admin permissions", () => {
    const adminUser: CurrentUser = {
      ID: "u-admin",
      Username: "admin",
      DisplayName: "Admin User",
      Roles: ["CustomAdmin"],
      Permissions: ["manage_channels"],
      HasAvatar: false,
    };

    expect(canAccessAdmin(adminUser)).toBe(true);
    expect(hasCapability(adminUser, "manage_channels")).toBe(true);
    expect(hasCapability(adminUser, "manage_members")).toBe(false);
  });
});
