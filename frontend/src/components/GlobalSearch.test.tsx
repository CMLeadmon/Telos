import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/api", () => ({
  api: vi.fn(),
}));

import { api } from "@/lib/api";
import { GlobalSearch } from "./GlobalSearch";

const apiMock = api as unknown as ReturnType<typeof vi.fn>;

describe("GlobalSearch", () => {
  beforeEach(() => {
    apiMock.mockReset();
  });

  it("renders search input with accessibility attributes", () => {
    render(<GlobalSearch />);
    expect(screen.getByTestId("global-search-input")).toBeInTheDocument();
    expect(screen.getByTestId("search-announcement")).toBeInTheDocument();
  });

  it("announces search result count to screen readers", async () => {
    apiMock.mockResolvedValue({
      items: [{ id: "1", title: "General Channel", type: "channel" }],
    });

    render(<GlobalSearch />);
    fireEvent.change(screen.getByTestId("global-search-input"), {
      target: { value: "general" },
    });

    await waitFor(() => {
      expect(screen.getByTestId("search-announcement")).toHaveTextContent(
        /1 search results found for general/i,
      );
    });
  });
});
