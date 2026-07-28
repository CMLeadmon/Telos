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
import { AdminChannelsSection } from "./AdminChannelsSection";

const apiMock = api as unknown as ReturnType<typeof vi.fn>;

beforeEach(() => {
  apiMock.mockReset();
  vi.spyOn(window, "confirm").mockReturnValue(true);
});

describe("AdminChannelsSection", () => {
  it("creates a channel and reloads the list", async () => {
    let created = false;
    apiMock.mockImplementation(async (path: string, init?: RequestInit) => {
      if (path === "/api/v1/admin/channels" && init?.method === "POST") {
        created = true;
        return { id: "c1" };
      }
      if (path === "/api/v1/channels") return created ? [{ id: "c1", name: "new-chan", type: "text" }] : [];
      return {};
    });
    render(<AdminChannelsSection />);
    fireEvent.change(screen.getByLabelText("Channel name"), { target: { value: "new-chan" } });
    fireEvent.click(screen.getByTestId("create-channel"));
    await waitFor(() => expect(screen.getByTestId("channel-c1")).toBeInTheDocument());
  });

  it("shows a visible error when a channel delete fails", async () => {
    apiMock.mockImplementation(async (path: string, init?: RequestInit) => {
      if (path === "/api/v1/channels") return [{ id: "c1", name: "some-chan" }];
      if (init?.method === "DELETE") throw new ApiError(500, "Could not delete the channel.");
      return {};
    });
    render(<AdminChannelsSection />);
    await waitFor(() => expect(screen.getByTestId("channel-c1")).toBeInTheDocument());
    fireEvent.click(screen.getByTestId("delete-channel-c1"));
    await waitFor(() => expect(screen.getByTestId("admin-channels-error")).toHaveTextContent(/could not delete/i));
  });

  it("renders per-role inherit/allow/deny override selects", async () => {
    apiMock.mockImplementation(async (path: string) => {
      if (path === "/api/v1/channels") return [{ id: "c1", name: "chan", type: "text" }];
      if (path.endsWith("/overrides")) return { overrides: { Member: { send_messages: "deny" } } };
      return {};
    });
    render(<AdminChannelsSection />);
    await waitFor(() => expect(screen.getByTestId("channel-c1")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Overrides"));
    await waitFor(() => expect(screen.getByTestId("override-editor")).toBeInTheDocument());
    const sel = screen.getByLabelText("Member send_messages") as HTMLSelectElement;
    expect(sel.value).toBe("deny");
  });
});
