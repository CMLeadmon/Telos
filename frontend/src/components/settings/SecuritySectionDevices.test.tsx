import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/api", () => ({
  api: vi.fn(),
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, m: string) {
      super(m);
      this.status = status;
    }
  },
}));

import { api, ApiError } from "@/lib/api";
import { SecuritySection } from "./SecuritySection";

const apiMock = api as unknown as ReturnType<typeof vi.fn>;

const DEVICE = {
  id: "dev-1",
  deviceName: "My iPhone",
  platform: "ios",
  clientVersion: "1.0.0",
  createdAt: "2026-08-01T00:00:00Z",
  lastSeenAt: "2026-08-06T12:00:00Z",
};

beforeEach(() => {
  apiMock.mockReset();
});

describe("SecuritySection registered devices", () => {
  it("lists devices and drops one after revoking it", async () => {
    let revoked = false;
    apiMock.mockImplementation(async (path: string, init?: RequestInit) => {
      if (path === "/api/v1/users/me/sessions") return [];
      if (path === "/api/v1/users/me/devices") return revoked ? [] : [DEVICE];
      if (path === "/api/v1/users/me/devices/dev-1" && init?.method === "DELETE") {
        revoked = true;
        return undefined;
      }
      throw new ApiError(404, "unexpected " + path);
    });

    render(<SecuritySection />);
    await waitFor(() => expect(screen.getByText("My iPhone")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: /revoke/i }));

    await waitFor(() =>
      expect(screen.queryByText("My iPhone")).not.toBeInTheDocument(),
    );
    expect(revoked).toBe(true);
  });

  // A device is not a session: it holds a 90-day refresh token that outlives
  // every sign-out, so it must be revoked through its own endpoint.
  it("revokes through the device endpoint, not the session one", async () => {
    apiMock.mockImplementation(async (path: string) => {
      if (path === "/api/v1/users/me/sessions") return [];
      if (path === "/api/v1/users/me/devices") return [DEVICE];
      return undefined;
    });

    render(<SecuritySection />);
    await waitFor(() => expect(screen.getByText("My iPhone")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /revoke/i }));

    await waitFor(() =>
      expect(apiMock).toHaveBeenCalledWith("/api/v1/users/me/devices/dev-1", {
        method: "DELETE",
      }),
    );
  });

  // An empty list and an unreachable node look the same on screen otherwise,
  // and the difference is whether a device is still out there holding a token.
  it("distinguishes no devices from an unreadable list", async () => {
    apiMock.mockImplementation(async (path: string) => {
      if (path === "/api/v1/users/me/sessions") return [];
      throw new ApiError(503, "node unavailable");
    });

    const { unmount } = render(<SecuritySection />);
    await waitFor(() =>
      expect(screen.getByText(/node unavailable/i)).toBeInTheDocument(),
    );
    expect(screen.queryByText(/no devices are registered/i)).not.toBeInTheDocument();
    unmount();

    apiMock.mockImplementation(async () => []);
    render(<SecuritySection />);
    await waitFor(() =>
      expect(screen.getByText(/no devices are registered/i)).toBeInTheDocument(),
    );
  });

  // Reloading after a failed revoke would put the device back on screen with no
  // explanation, reading as though the click did nothing at all.
  it("keeps the device and explains itself when revocation fails", async () => {
    apiMock.mockImplementation(async (path: string, init?: RequestInit) => {
      if (path === "/api/v1/users/me/sessions") return [];
      if (path === "/api/v1/users/me/devices") return [DEVICE];
      if (init?.method === "DELETE") throw new ApiError(500, "revocation failed");
      return undefined;
    });

    render(<SecuritySection />);
    await waitFor(() => expect(screen.getByText("My iPhone")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /revoke/i }));

    await waitFor(() =>
      expect(screen.getByText(/revocation failed/i)).toBeInTheDocument(),
    );
    expect(screen.getByText("My iPhone")).toBeInTheDocument();
  });
});
