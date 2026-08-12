import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/api", () => ({
  api: vi.fn(),
  apiBase: () => "https://telos.example.com",
  // Stands in for the real one, which prepends apiBase to a relative path.
  assetUrl: (p: string) => (p?.startsWith("http") ? p : `https://telos.example.com${p}`),
}));

import { api } from "@/lib/api";
import { AudiobookPlayer } from "./AudiobookPlayer";

const apiMock = api as unknown as ReturnType<typeof vi.fn>;

const INFO = {
  id: "11111111-1111-4111-8111-111111111111",
  title: "Beyond Good and Evil",
  author: "Nietzsche",
  narrator: "A Narrator",
  durationMs: 7200000,
  codec: "mp3",
  tracks: [
    { index: 0, title: "Part One", durationMs: 3600000, cumulativeStartMs: 0 },
    { index: 1, title: "Part Two", durationMs: 3600000, cumulativeStartMs: 3600000 },
  ],
  chapters: [],
  progress: { locator: { positionMs: 900000, trackIndex: 1 }, percent: 0.25, completed: false },
};

function routeApi(info: unknown = INFO) {
  apiMock.mockImplementation((path: string) => {
    if (path.includes("/info")) return Promise.resolve(info);
    return Promise.resolve({});
  });
}

describe("AudiobookPlayer", () => {
  beforeEach(() => {
    apiMock.mockReset();
    routeApi();
  });

  // The player reached for api.get/api.put, which do not exist on the client —
  // `api` is a plain function. Every call threw before it reached the network.
  it("calls the gateway through the real client, not a method that does not exist", async () => {
    render(<AudiobookPlayer itemId={INFO.id} onClose={vi.fn()} />);

    await screen.findByText("Beyond Good and Evil");
    expect(apiMock).toHaveBeenCalledWith(
      `/api/v1/library/audiobooks/${INFO.id}/info`,
    );
    // Nothing may reach for a method on the client.
    expect((api as unknown as Record<string, unknown>).get).toBeUndefined();
    expect((api as unknown as Record<string, unknown>).put).toBeUndefined();
  });

  // The saved locator restored the track but never the offset within it, so a
  // resume silently restarted the track from zero.
  it("resumes at the saved track and offset", async () => {
    render(<AudiobookPlayer itemId={INFO.id} onClose={vi.fn()} />);

    const track = await screen.findByRole("button", { name: /Part Two/ });
    expect(track).toHaveAttribute("aria-current", "true");

    const audio = document.querySelector("audio") as HTMLAudioElement;
    expect(audio.getAttribute("src")).toContain("/tracks/1/stream");

    // jsdom has no media stack, so duration is stubbed to let the seek apply.
    Object.defineProperty(audio, "duration", { value: 3600, configurable: true });
    fireEvent.loadedMetadata(audio);

    await waitFor(() => expect(audio.currentTime).toBe(900));
  });

  it("saves progress through a PUT with the current track in the locator", async () => {
    render(<AudiobookPlayer itemId={INFO.id} onClose={vi.fn()} />);
    await screen.findByText("Beyond Good and Evil");

    const audio = document.querySelector("audio") as HTMLAudioElement;
    Object.defineProperty(audio, "currentTime", { value: 42, configurable: true, writable: true });
    fireEvent.pause(audio);

    await waitFor(() => {
      const put = apiMock.mock.calls.find(
        (c) => (c[1] as RequestInit | undefined)?.method === "PUT",
      );
      expect(put).toBeDefined();
      expect(put![0]).toBe(`/api/v1/library/books/${INFO.id}/progress`);
      const body = JSON.parse((put![1] as RequestInit).body as string);
      expect(body.locator.trackIndex).toBe(1);
      expect(body.completed).toBe(false);
    });
  });

  it("reports a failed load instead of rendering an empty player", async () => {
    apiMock.mockImplementation(() => Promise.reject(new Error("Grimmory unavailable")));
    render(<AudiobookPlayer itemId={INFO.id} onClose={vi.fn()} />);

    expect(await screen.findByText("Grimmory unavailable")).toBeInTheDocument();
  });

  // An <audio> element fetches its own source, so a relative src resolves
  // against whatever document loaded it — in the native client, the app bundle,
  // where there is no audiobook. This shipped that way through all of S3.
  // Track 1, because the fixture resumes there.
  it("points the audio element at the node, not at the document", async () => {
    apiMock.mockImplementation(() => Promise.resolve(INFO));
    const { container } = render(<AudiobookPlayer itemId={INFO.id} onClose={vi.fn()} />);

    await waitFor(() => {
      const audio = container.querySelector("audio");
      expect(audio?.getAttribute("src")).toBe(
        `https://telos.example.com/api/v1/library/audiobooks/${INFO.id}/tracks/1/stream`,
      );
    });
  });
});
