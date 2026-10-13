"use client";

import { useEffect, useRef, useState } from "react";
import { PictureInPicture2 } from "lucide-react";
import type Hls from "hls.js";
import { getServerConfig } from "@/lib/serverConfig";
import { mintAudioTicket, mintVideoLocator } from "@/lib/mediaLocator";

const RATES = [1, 1.25, 1.5, 2] as const;

// HlsPlayer plays a Jellyfin item. It never loads a protected route directly: a
// media element fetches its own source and cannot attach an Authorization header,
// so it first asks the node (authenticated) for a self-authorizing URL --
// an HLS locator for video, a stream ticket for audio -- and loads that. hls.js
// gets the same URL and no bearer injection, because the locator is the
// credential. If minting fails, nothing is assigned to the element: falling back
// to the protected route would just produce a silent 401.
//
// A locator lives at most 12 hours. If a long session outlives one, a fatal
// network error re-mints and resumes where playback stopped, a bounded number of
// times, rather than leaving a dead player.
const MAX_REMINTS = 3;

export function HlsPlayer({ itemId, audio }: { itemId: string; audio: boolean }) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const [error, setError] = useState<string | null>(null);
  const [rate, setRate] = useState(1);

  const changeRate = (r: number) => {
    setRate(r);
    if (videoRef.current) videoRef.current.playbackRate = r;
  };

  // Picture-in-Picture is a genuine mobile-web capability (unlike a gesture
  // layer), so surface it explicitly for video. It is a no-op where the browser
  // does not support it.
  const canPip =
    !audio && typeof document !== "undefined" && "pictureInPictureEnabled" in document;
  const togglePip = () => {
    const v = videoRef.current;
    if (!v) return;
    if (document.pictureInPictureElement) {
      void document.exitPictureInPicture().catch(() => {});
    } else if (document.pictureInPictureEnabled) {
      void v.requestPictureInPicture().catch(() => {});
    }
  };

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    setError(null);

    const controller = new AbortController();
    let cancelled = false;
    let hls: Hls | null = null;
    let reminted = 0;

    const mint = () => (audio ? mintAudioTicket(itemId, controller.signal) : mintVideoLocator(itemId, controller.signal));
    const fail = (message: string) => {
      if (!cancelled) setError(message);
    };

    void (async () => {
      let locator;
      try {
        locator = await mint();
      } catch {
        // Bounded and generic: the address itself is a credential and the node's
        // reason is not the member's business to decode.
        fail("playback could not be authorized");
        return;
      }
      if (cancelled) return;

      // Audio and native HLS (Safari, WebKitGTK with GStreamer) play the URL
      // directly: the locator carries its own authority.
      if (audio || video.canPlayType("application/vnd.apple.mpegurl")) {
        video.src = locator.url;
        return;
      }

      const { default: HlsCtor } = await import("hls.js");
      if (cancelled) return;
      if (!HlsCtor.isSupported()) {
        fail("hls playback is not supported in this browser");
        return;
      }
      // No xhrSetup: the locator is the credential, and attaching the access
      // token to a bare locator request would only leak it for nothing.
      hls = new HlsCtor();
      hls.on(HlsCtor.Events.ERROR, (_event, data) => {
        if (!data.fatal) return;
        const resumeAt = video.currentTime;
        const network = data.type === HlsCtor.ErrorTypes.NETWORK_ERROR;
        if (network && reminted < MAX_REMINTS) {
          reminted += 1;
          void mint()
            .then((next) => {
              if (cancelled || !hls) return;
              hls.loadSource(next.url);
              hls.startLoad(resumeAt);
            })
            .catch(() => fail(`stream failed: ${data.details}`));
          return;
        }
        fail(`stream failed: ${data.details}`);
        hls?.destroy();
      });
      hls.loadSource(locator.url);
      hls.attachMedia(video);
    })();

    return () => {
      cancelled = true;
      controller.abort();
      hls?.destroy();
      video.removeAttribute("src");
      video.load();
    };
  }, [itemId, audio]);

  return (
    <div className="playerstage">
      {error ? (
        <p className="playererr">{`// ${error}`}</p>
      ) : (
        <>
          <video
            ref={videoRef}
            controls
            autoPlay
            playsInline
            crossOrigin={getServerConfig().baseUrl ? "anonymous" : undefined}
            data-testid="stream-video"
          />
          <div className="player-extras">
            <div className="player-rate" role="group" aria-label="Playback speed">
              {RATES.map((r) => (
                <button
                  key={r}
                  className={`rate-btn${rate === r ? " on" : ""}`}
                  aria-pressed={rate === r}
                  onClick={() => changeRate(r)}
                >
                  {r}×
                </button>
              ))}
            </div>
            {canPip && (
              <button
                className="iconbtn"
                aria-label="picture in picture"
                onClick={togglePip}
              >
                <PictureInPicture2 size={18} />
              </button>
            )}
          </div>
        </>
      )}
    </div>
  );
}
