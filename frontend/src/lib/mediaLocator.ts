// Minting self-authorizing media URLs.
//
// A media element fetches its own source and nothing can attach an Authorization
// header to that request, so playback needs a URL that carries its own authority:
// a capability the node mints for the signed-in member and re-checks on every
// range request (backend/media_authority.go). This module asks for one through
// the authenticated client and returns an absolute URL on the CONFIGURED node.
//
// The minted URL is a credential. It is never logged, never stored, and the
// response that carried it is not cached by the node.

import { api, apiBase } from "./api";

export interface MediaLocator {
  /** Absolute URL a media element or hls.js can load with no credentials. */
  url: string;
  /** Seconds until the node stops honouring it. */
  expiresIn: number;
}

interface MintResponse {
  url?: unknown;
  expiresIn?: unknown;
}

const LOCATOR_PATHS = [/^\/api\/v1\/hls\/[A-Za-z0-9_.-]+$/, /^\/api\/v1\/stream\/audio-ticket\/[A-Za-z0-9_.-]+$/];

/**
 * Resolves a node-relative locator against the configured node. Anything that is
 * not exactly one of the node's capability paths is refused, so a hostile or
 * buggy response cannot make the player load an arbitrary URL.
 */
export function resolveLocatorUrl(relative: unknown): string {
  if (typeof relative !== "string" || !LOCATOR_PATHS.some((pattern) => pattern.test(relative))) {
    throw new Error("The node returned an unexpected playback address.");
  }
  return `${apiBase()}${relative}`;
}

async function mint(path: string, signal?: AbortSignal): Promise<MediaLocator> {
  const res = await api<MintResponse>(path, { method: "POST", signal });
  const expiresIn = typeof res?.expiresIn === "number" ? res.expiresIn : 0;
  return { url: resolveLocatorUrl(res?.url), expiresIn };
}

/** A bare, short-of-12-hours HLS entry URL for a Stream video item. */
export function mintVideoLocator(itemId: string, signal?: AbortSignal): Promise<MediaLocator> {
  return mint(`/api/v1/stream/video/${encodeURIComponent(itemId)}/playback-locator`, signal);
}

/** A bare direct-audio URL for a Stream audio item. */
export function mintAudioTicket(itemId: string, signal?: AbortSignal): Promise<MediaLocator> {
  return mint(`/api/v1/stream/audio/${encodeURIComponent(itemId)}/stream-ticket`, signal);
}
