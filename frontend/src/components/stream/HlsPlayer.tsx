"use client";

import { useEffect, useRef, useState } from "react";
import type Hls from "hls.js";
import { apiBase } from "@/lib/api";

// HlsPlayer plays a Jellyfin stream. Video items are HLS: the gateway resolves
// PlaybackInfo and 302s to a main.m3u8 sub-path; hls.js follows the redirect and
// resolves segments against the final URL, while Safari plays it natively. Audio
// is a static proxy the element can load directly.
export function HlsPlayer({ src, audio }: { src: string; audio: boolean }) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    setError(null);

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
      hls = new HlsCtor({
        // Include the session cookie on manifest and segment requests.
        xhrSetup: (xhr) => {
          xhr.withCredentials = true;
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
        <video
          ref={videoRef}
          controls
          autoPlay
          playsInline
          crossOrigin={apiBase() ? "use-credentials" : undefined}
          data-testid="stream-video"
        />
      )}
    </div>
  );
}
