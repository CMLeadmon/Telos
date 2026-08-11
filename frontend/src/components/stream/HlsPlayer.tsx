"use client";

import { useEffect, useRef, useState } from "react";
import { PictureInPicture2 } from "lucide-react";
import type Hls from "hls.js";
import { getServerConfig } from "@/lib/serverConfig";

const RATES = [1, 1.25, 1.5, 2] as const;

// HlsPlayer plays a Jellyfin stream. Video items are HLS: the gateway resolves
// PlaybackInfo and 302s to a main.m3u8 sub-path; hls.js follows the redirect and
// resolves segments against the final URL, while Safari plays it natively. Audio
// is a static proxy the element can load directly.
export function HlsPlayer({ src, audio }: { src: string; audio: boolean }) {
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

    // Token mode does not reach this path. A media element fetches its own
    // source and there is no hook to attach an Authorization header, so audio
    // and native HLS (Safari) can only authenticate by cookie or by a
    // credential carried in the URL itself. The xhrSetup below covers hls.js
    // because that goes through XHR; this branch does not. Remote playback
    // needs a signed locator — the gateway already signs HLS locators — and
    // that belongs to S4, not here.
    if (audio || video.canPlayType("application/vnd.apple.mpegurl")) {
      video.src = src;
      return () => {
        video.removeAttribute("src");
        video.load();
      };
    }

    let hls: Hls | null = null;
    let cancelled = false;
    void import("hls.js").then(({ default: HlsCtor }) => {
      if (cancelled) return;
      if (!HlsCtor.isSupported()) {
        setError("hls playback is not supported in this browser");
        return;
      }
      const cfg = getServerConfig();
      hls = new HlsCtor({
        xhrSetup: (xhr) => {
          if (cfg.mode === "token") {
            if (cfg.accessToken) {
              xhr.setRequestHeader("Authorization", `Bearer ${cfg.accessToken}`);
            }
          } else {
            xhr.withCredentials = true;
          }
        },
      });
      hls.on(HlsCtor.Events.ERROR, (_event, data) => {
        if (data.fatal) {
          setError(`stream failed: ${data.details}`);
          hls?.destroy();
        }
      });
      hls.loadSource(src);
      hls.attachMedia(video);
    });

    return () => {
      cancelled = true;
      hls?.destroy();
    };
  }, [src, audio]);

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
            crossOrigin={
              getServerConfig().baseUrl
                ? getServerConfig().mode === "token"
                  ? "anonymous"
                  : "use-credentials"
                : undefined
            }
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
