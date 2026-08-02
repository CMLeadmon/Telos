import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const push = vi.fn();

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push }),
}));

import { ShareCard } from "./ShareCard";

beforeEach(() => {
  push.mockReset();
});

describe("ShareCard", () => {
  it.each([
    ["library_book" as const, "Read", "/library/?read=opaque%2Fbook%20%3Fpart%3D1"],
    ["stream_film" as const, "Stream", "/stream/?play=opaque%2Fbook%20%3Fpart%3D1"],
  ])("encodes an opaque %s reference in its action URL", (kind, action, expected) => {
    render(
      <ShareCard
        embed={{
          kind,
          ref: "opaque/book ?part=1",
          snapshot: { title: "Shared item", subtitle: "", kicker: "", cover: "" },
        }}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: action }));

    expect(push).toHaveBeenCalledWith(expected);
  });
});
