// Trust-on-first-use pinning of a Telos node's TLS leaf certificate.
//
// Pinning requires the peer's actual certificate. No web API exposes it: fetch,
// XHR and WebSocket all complete the TLS handshake inside the browser and give
// the page no access to the chain. So this can only work where a native layer
// performs the handshake and reports the fingerprint back — the Tauri shell in
// S5. In a plain browser there is nothing to pin, and the honest answer is to
// say so rather than to invent a value.
//
// That distinction is the whole point of this module. An earlier draft returned
// SHA-256("telos-cert:" + host): a pure function of the hostname that never
// looked at a certificate at all. It reported "valid" for any certificate
// presented for that host, which is precisely the interception TOFU exists to
// detect, while the UI told the member their connection was pinned. A security
// control that cannot fail is worse than none, because it is trusted.

export type CertTrustStatus =
  /** No native TLS bridge. The platform validated the chain normally; nothing is pinned. */
  | "unsupported"
  /** The presented fingerprint matches the pinned one. */
  | "trusted"
  /** Nothing pinned yet — the caller must show the fingerprint and ask the member to confirm. */
  | "first-use"
  /** The presented fingerprint differs from the pinned one. Block the connection. */
  | "mismatch";

export interface CertVerification {
  status: CertTrustStatus;
  /** null whenever no certificate was actually inspected. Never synthesized. */
  fingerprint: string | null;
  subject: string | null;
  issuer: string | null;
}

/**
 * The contract the native shell must satisfy for pinning to exist. S5 has to
 * implement this; until it does, every client reports "unsupported" and the
 * connect screen must not claim otherwise.
 */
export interface NativeTlsBridge {
  leafCertificate(serverUrl: string): Promise<{
    fingerprintSha256: string;
    subject?: string;
    issuer?: string;
  } | null>;
}

interface NativeWindow {
  __TELOS_NATIVE_TLS__?: NativeTlsBridge;
}

function nativeTls(): NativeTlsBridge | null {
  if (typeof window === "undefined") return null;
  return (window as unknown as NativeWindow).__TELOS_NATIVE_TLS__ ?? null;
}

/** Whether this build can pin at all. The connect UI keys its wording off this. */
export function certPinningAvailable(): boolean {
  return nativeTls() !== null;
}

/**
 * Fingerprints get written as "AA:BB:CC", "aabbcc", or with a "sha256:" prefix
 * depending on who printed them. Comparing the raw strings would report a
 * mismatch on formatting alone and block a legitimate connection — a false
 * alarm on a security screen teaches people to click through it.
 */
export function normalizeFingerprint(raw: string): string {
  return raw.trim().toLowerCase().replace(/^sha-?256[:=]/, "").replace(/[\s:]/g, "");
}

export async function verifyCertificate(
  serverUrl: string,
  pinnedFingerprint: string | null,
): Promise<CertVerification> {
  const unsupported: CertVerification = {
    status: "unsupported",
    fingerprint: null,
    subject: null,
    issuer: null,
  };

  const bridge = nativeTls();
  if (!bridge) return unsupported;

  let parsed: URL;
  try {
    parsed = new URL(serverUrl);
  } catch {
    return unsupported;
  }
  // Plain HTTP has no certificate. Reporting "trusted" here would be the same
  // lie in a different shape.
  if (parsed.protocol !== "https:") return unsupported;

  let leaf: Awaited<ReturnType<NativeTlsBridge["leafCertificate"]>>;
  try {
    leaf = await bridge.leafCertificate(serverUrl);
  } catch {
    return unsupported;
  }
  if (!leaf?.fingerprintSha256) return unsupported;

  const fingerprint = normalizeFingerprint(leaf.fingerprintSha256);
  if (!fingerprint) return unsupported;

  const details = {
    fingerprint,
    subject: leaf.subject ?? null,
    issuer: leaf.issuer ?? null,
  };

  if (!pinnedFingerprint) return { status: "first-use", ...details };
  return {
    status:
      fingerprint === normalizeFingerprint(pinnedFingerprint)
        ? "trusted"
        : "mismatch",
    ...details,
  };
}
