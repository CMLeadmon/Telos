import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/api", () => ({
  api: vi.fn(),
  apiBase: () => "https://telos.example.com",
  // Stands in for the real one, which prepends apiBase to a relative path.
  assetUrl: (p: string) => (p?.startsWith("http") ? p : `https://telos.example.com${p}`),
}));

import { api } from "@/lib/api";
import { setServerConfig } from "@/lib/serverConfig";
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

type ActionDetails = { seekOffset?: number };
type Handler = ((details: ActionDetails) => void) | null;

let handlers: Map<string, Handler>;
let session: {
  metadata: { title: string; artist: string; album: string; artwork: { src: string }[] } | null;
  playbackState: string;
  setActionHandler: ReturnType<typeof vi.fn>;
};

/** Drives a registered handler the way the OS transport would. */
function invoke(action: string, details: ActionDetails = {}) {
  const handler = handlers.get(action);
  expect(handler, `no handler registered for "${action}"`).toBeTypeOf("function");
  handler!(details);
}

/** jsdom has no media stack, so the element's clock is stubbed in by hand. */
function stubAudioClock(audio: HTMLMediaElement, currentTime: number, duration = 3600) {
  Object.defineProperty(audio, "duration", { value: duration, configurable: true });
  Object.defineProperty(audio, "currentTime", {
    value: currentTime,
    configurable: true,
    writable: true,
  });
}

// The OS transport — lock screen, headset button, car head unit — reaches a web
// player through navigator.mediaSession and through nothing else. In a native
// shell that is most of how an audiobook is actually listened to: the screen is
// off and the app is backgrounded.
describe("AudiobookPlayer MediaSession integration", () => {
  beforeEach(() => {
    apiMock.mockReset();
    apiMock.mockImplementation((path: string) =>
      path.includes("/info") ? Promise.resolve(INFO) : Promise.resolve({}),
    );
    setServerConfig({ baseUrl: "", mode: "cookie", accessToken: null });

    handlers = new Map();
    session = {
      metadata: null,
      playbackState: "none",
      setActionHandler: vi.fn((action: string, handler: Handler) => {
        handlers.set(action, handler);
      }),
    };
    Object.defineProperty(navigator, "mediaSession", {
      value: session,
      configurable: true,
      writable: true,
    });
    vi.stubGlobal(
      "MediaMetadata",
      class {
        constructor(init: Record<string, unknown>) {
          Object.assign(this, init);
        }
      },
    );
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    Reflect.deleteProperty(navigator, "mediaSession");
  });

  it("publishes the playing track to the OS once info loads", async () => {
    render(<AudiobookPlayer itemId={INFO.id} onClose={vi.fn()} />);
    await screen.findByText("Beyond Good and Evil");

    await waitFor(() => expect(session.metadata).not.toBeNull());
    // The resumed track, not the book, is what the lock screen names — the book
    // is the album.
    expect(session.metadata!.title).toBe("Part Two");
    expect(session.metadata!.artist).toBe("Nietzsche");
    expect(session.metadata!.album).toBe("Beyond Good and Evil");
  });

  // The OS fetches artwork itself, out of the page, so a relative path resolves
  // against the native shell's own bundle rather than against the node.
  it("gives the OS an absolute artwork URL pointing at the node", async () => {
    render(<AudiobookPlayer itemId={INFO.id} onClose={vi.fn()} />);
    await waitFor(() => expect(session.metadata).not.toBeNull());

    expect(session.metadata!.artwork[0].src).toBe(
      `https://telos.example.com/api/v1/library/books/${INFO.id}/cover`,
    );
  });

  it("registers the transport controls a backgrounded player needs", async () => {
    render(<AudiobookPlayer itemId={INFO.id} onClose={vi.fn()} />);
    await waitFor(() => expect(handlers.size).toBeGreaterThan(0));

    expect([...handlers.keys()].sort()).toEqual([
      "pause",
      "play",
      "seekbackward",
      "seekforward",
    ]);
  });

  it("moves the audio element when the OS asks it to pause", async () => {
    const pause = vi
      .spyOn(HTMLMediaElement.prototype, "pause")
      .mockImplementation(() => {});
    render(<AudiobookPlayer itemId={INFO.id} onClose={vi.fn()} />);
    await waitFor(() => expect(handlers.has("pause")).toBe(true));

    invoke("pause");

    expect(pause).toHaveBeenCalled();
    pause.mockRestore();
  });

  it("seeks by the offset the OS supplies, and by the player's own step when it supplies none", async () => {
    const { container } = render(<AudiobookPlayer itemId={INFO.id} onClose={vi.fn()} />);
    await waitFor(() => expect(handlers.has("seekbackward")).toBe(true));
    const audio = container.querySelector("audio") as HTMLMediaElement;

    stubAudioClock(audio, 100);
    invoke("seekbackward", { seekOffset: 30 });
    expect(audio.currentTime).toBe(70);

    stubAudioClock(audio, 100);
    invoke("seekforward");
    // Matches the ±15s the on-screen skip buttons use.
    expect(audio.currentTime).toBe(115);
  });

  it("does not seek past the ends of the track", async () => {
    const { container } = render(<AudiobookPlayer itemId={INFO.id} onClose={vi.fn()} />);
    await waitFor(() => expect(handlers.has("seekbackward")).toBe(true));
    const audio = container.querySelector("audio") as HTMLMediaElement;

    stubAudioClock(audio, 5);
    invoke("seekbackward");
    expect(audio.currentTime).toBe(0);

    stubAudioClock(audio, 3595);
    invoke("seekforward");
    expect(audio.currentTime).toBe(3600);
  });

  // A lock screen showing "play" while audio is coming out of the speaker is
  // worse than showing nothing: the button the member presses does the opposite
  // of what it says.
  it("mirrors playback state so the lock screen button matches reality", async () => {
    const { container } = render(<AudiobookPlayer itemId={INFO.id} onClose={vi.fn()} />);
    await screen.findByText("Beyond Good and Evil");
    const audio = container.querySelector("audio") as HTMLMediaElement;

    fireEvent.play(audio);
    await waitFor(() => expect(session.playbackState).toBe("playing"));

    fireEvent.pause(audio);
    await waitFor(() => expect(session.playbackState).toBe("paused"));
  });

  // The session is process-wide. A closed player that stayed registered would
  // keep answering the lock screen with an element no longer on the page.
  it("releases the transport controls when the player closes", async () => {
    const { unmount } = render(<AudiobookPlayer itemId={INFO.id} onClose={vi.fn()} />);
    await waitFor(() => expect(handlers.has("play")).toBe(true));

    unmount();

    expect([...handlers.values()].every((h) => h === null)).toBe(true);
  });

  // Clearing the handlers is not enough on its own: metadata and playbackState
  // are separate properties, and a closed player that left them set shows the
  // book on the lock screen, paused, with buttons that now do nothing.
  it("takes the book off the lock screen when the player closes", async () => {
    const { unmount } = render(<AudiobookPlayer itemId={INFO.id} onClose={vi.fn()} />);
    await waitFor(() => expect(session.metadata).not.toBeNull());

    unmount();

    expect(session.metadata).toBeNull();
    expect(session.playbackState).toBe("none");
  });

  // Only on unmount. Clearing on every metadata change would blank the state
  // mid-book: the track effect re-registers, but the playbackState effect does
  // not re-run for a track change, so "none" would stick while audio played.
  it("keeps the lock screen live across a track change", async () => {
    const { container } = render(<AudiobookPlayer itemId={INFO.id} onClose={vi.fn()} />);
    const audio = (await screen.findByText("Beyond Good and Evil"),
      container.querySelector("audio")) as HTMLMediaElement;
    fireEvent.play(audio);
    await waitFor(() => expect(session.playbackState).toBe("playing"));

    fireEvent.click(await screen.findByRole("button", { name: /Part One/ }));

    await waitFor(() => expect(session.metadata?.title).toBe("Part One"));
    expect(session.playbackState).toBe("playing");
  });

  // Safari before 15 and every non-browser test environment lack it. Reaching
  // for the constructor unguarded throws out of the effect and takes the whole
  // player down with it.
  it("still renders where the browser has no MediaSession", async () => {
    Reflect.deleteProperty(navigator, "mediaSession");
    vi.stubGlobal("MediaMetadata", undefined);

    render(<AudiobookPlayer itemId={INFO.id} onClose={vi.fn()} />);

    expect(await screen.findByText("Beyond Good and Evil")).toBeInTheDocument();
  });
});
