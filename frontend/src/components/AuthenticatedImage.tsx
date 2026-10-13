"use client";

import { useEffect, useState, type ImgHTMLAttributes } from "react";
import { ApiError, apiBlob, assetUrl } from "@/lib/api";
import { createTrackedObjectUrl, revokeTrackedObjectUrl } from "@/lib/objectUrls";
import { getServerConfig } from "@/lib/serverConfig";

// Protected node images (covers, avatars) cannot be loaded by a plain <img> in
// the native client: it holds no cookie and an image element cannot send an
// Authorization header. This component fetches the bytes through the
// authenticated client and shows them from a blob URL it revokes on replacement,
// unmount and sign-out. In the browser the cookie already authorizes the request,
// so it renders an ordinary <img> and nothing changes.

const MAX_IMAGE_BYTES = 8 * 1024 * 1024;

type Props = Omit<ImgHTMLAttributes<HTMLImageElement>, "src" | "onError"> & {
  /** A node path ("/api/v1/...") or an absolute URL. */
  path: string | null | undefined;
  /** Called when the image cannot be fetched or decoded. */
  onError?: () => void;
};

/** True only for a path on the node itself; never for another origin. */
export function isNodePath(path: string): boolean {
  if (path.startsWith("//")) return false;
  if (path.startsWith("/")) return true;
  const base = getServerConfig().baseUrl;
  if (!base) return false;
  try {
    return new URL(path).origin === new URL(base).origin;
  } catch {
    return false;
  }
}

export function AuthenticatedImage({ path, alt = "", onError, ...rest }: Props) {
  const tokenMode = getServerConfig().mode === "token";
  const nodePath = path ? isNodePath(path) : false;
  const needsFetch = tokenMode && nodePath;
  const [loaded, setLoaded] = useState<{ path: string; url: string } | null>(null);
  const [failed, setFailed] = useState<string | null>(null);

  useEffect(() => {
    if (!needsFetch || !path) return;
    let cancelled = false;
    let created: string | null = null;
    apiBlob(path.startsWith("/") ? path : new URL(path).pathname + new URL(path).search)
      .then((blob) => {
        if (blob.size > MAX_IMAGE_BYTES || (blob.type && !blob.type.startsWith("image/"))) {
          throw new ApiError(415, "Not a displayable image.");
        }
        if (cancelled) return;
        created = createTrackedObjectUrl(blob);
        setFailed(null);
        setLoaded({ path, url: created });
      })
      .catch(() => {
        if (cancelled) return;
        setFailed(path);
        onError?.();
      });
    return () => {
      cancelled = true;
      if (created) revokeTrackedObjectUrl(created);
    };
    // onError is a callback, not an input: refetching when its identity changes would loop.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [needsFetch, path]);

  if (!path) return null;
  if (needsFetch) {
    // Nothing is shown for a path other than the one that was fetched, so a stale
    // image never lingers under a new item.
    if (failed === path) return null;
    if (loaded?.path !== path) return <span className="authimg-pending" aria-hidden="true" />;
    // eslint-disable-next-line @next/next/no-img-element
    return <img {...rest} alt={alt} src={loaded.url} onError={onError} />;
  }
  // An off-node image is requested without any credential and without leaking the
  // node address as a referrer; in the browser a node image rides the cookie.
  // eslint-disable-next-line @next/next/no-img-element
  return <img {...rest} alt={alt} src={assetUrl(path)} onError={onError} referrerPolicy={nodePath ? rest.referrerPolicy : "no-referrer"} />;
}
