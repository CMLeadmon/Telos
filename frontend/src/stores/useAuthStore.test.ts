import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/api", () => ({
  api: vi.fn(),
  ApiError: class ApiError extends Error {
    constructor(public status: number, message: string) {
      super(message);
    }
  },
}));

import { api, ApiError } from "@/lib/api";
import { useAuthStore } from "./useAuthStore";

const apiMock = api as unknown as ReturnType<typeof vi.fn>;

describe("useAuthStore failure recovery", () => {
  beforeEach(() => {
    apiMock.mockReset();
    useAuthStore.setState({ user: null, status: "unknown", connectivity: "online" });
  });

  it("sets user and authenticated status on fetchMe success", async () => {
    const user = {
      ID: "u1",
      Username: "user1",
      Roles: ["Member"],
      Permissions: ["view_channel"],
      DisplayName: "User One",
      HasAvatar: false,
    };
    apiMock.mockResolvedValue(user);

    await useAuthStore.getState().fetchMe();

    expect(useAuthStore.getState().user).toEqual(user);
    expect(useAuthStore.getState().status).toBe("authenticated");
    expect(useAuthStore.getState().connectivity).toBe("online");
  });

  it("sets anonymous status and clears user on explicit 401 response", async () => {
    apiMock.mockRejectedValue(new ApiError(401, "Unauthorized"));

    await useAuthStore.getState().fetchMe();

    expect(useAuthStore.getState().user).toBeNull();
    expect(useAuthStore.getState().status).toBe("anonymous");
    expect(useAuthStore.getState().connectivity).toBe("online");
  });

  it("preserves last known authenticated user on transient non-401 error (500 / network)", async () => {
    const user = {
      ID: "u1",
      Username: "user1",
      Roles: ["Member"],
      Permissions: ["view_channel"],
      DisplayName: "User One",
      HasAvatar: false,
    };
    useAuthStore.setState({ user, status: "authenticated", connectivity: "online" });

    apiMock.mockRejectedValue(new ApiError(500, "Internal Server Error"));

    await useAuthStore.getState().fetchMe();

    // User state is preserved, connectivity marked error
    expect(useAuthStore.getState().user).toEqual(user);
    expect(useAuthStore.getState().status).toBe("authenticated");
    expect(useAuthStore.getState().connectivity).toBe("error");
  });
});
