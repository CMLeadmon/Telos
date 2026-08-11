"use client";

import React, { useState } from "react";
import { useRouter } from "next/navigation";
import { BrandLogo } from "@/components/BrandLogo";
import { useConnectionStore } from "@/stores/useConnectionStore";
import { ApiError, probeNode } from "@/lib/api";
import { normalizeBaseUrl } from "@/lib/serverConfig";
import { CLIENT_VERSION, clientBelowMinimum, compareVersions } from "@/lib/clientVersion";
import {
  certPinningAvailable,
  verifyCertificate,
  type CertVerification,
} from "@/lib/certPinning";
import { getSecret, setSecret } from "@/lib/secureStorage";

/** Pins are stored per host, because that is what a certificate is issued for. */
function pinKey(serverUrl: string): string {
  try {
    return `cert-pin:${new URL(serverUrl).host}`;
  } catch {
    return `cert-pin:${serverUrl}`;
  }
}

/** Grouped four at a time so a member can actually read a fingerprint aloud. */
function formatFingerprint(fingerprint: string): string {
  return (fingerprint.match(/.{1,4}/g) ?? [fingerprint]).join(" ");
}

export default function ConnectPage() {
  const router = useRouter();
  const [urlInput, setUrlInput] = useState("");
  const [loading, setLoading] = useState(false);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  const [pendingTrust, setPendingTrust] = useState<{
    serverUrl: string;
    version: string | null;
    cert: CertVerification;
  } | null>(null);

  const setValidating = useConnectionStore((s) => s.setValidating);
  const setConfigured = useConnectionStore((s) => s.setConfigured);
  const setError = useConnectionStore((s) => s.setError);
  const setVersionSkewSoft = useConnectionStore((s) => s.setVersionSkewSoft);

  const finish = async (
    serverUrl: string,
    version: string | null,
    cert: CertVerification,
  ) => {
    if (cert.fingerprint) {
      await setSecret(pinKey(serverUrl), cert.fingerprint);
    }
    setConfigured(cert.fingerprint ?? undefined, version ?? undefined);
    // Informational only: a node ahead of this client may expose things it
    // cannot render. A client behind the node's stated minimum was already
    // rejected above.
    setVersionSkewSoft(!!version && compareVersions(version, CLIENT_VERSION) > 0);
    router.push("/login/");
  };

  const handleConnect = async (e: React.FormEvent) => {
    e.preventDefault();
    setErrorMsg(null);
    setPendingTrust(null);
    if (!urlInput.trim()) return;

    // A bare host is far and away what people type. Default to https, never
    // http — silently downgrading the transport is not ours to decide.
    const typed = urlInput.trim();
    const candidate = /^https?:\/\//i.test(typed) ? typed : `https://${typed}`;

    let serverUrl: string;
    try {
      serverUrl = normalizeBaseUrl(candidate);
    } catch {
      setErrorMsg("That is not a valid server address.");
      return;
    }

    setLoading(true);
    setValidating(serverUrl);
    try {
      const probe = await probeNode(serverUrl);

      if (clientBelowMinimum(probe.minClientVersion)) {
        const message = `This node requires client ${probe.minClientVersion} or newer; this client is ${CLIENT_VERSION}.`;
        setError("version_skew_hard", message);
        setErrorMsg(message);
        return;
      }

      const pinned = await getSecret(pinKey(serverUrl));
      const cert = await verifyCertificate(serverUrl, pinned);

      if (cert.status === "mismatch") {
        const message =
          "This node presented a different certificate than the one trusted before. Someone may be intercepting the connection.";
        setError("untrusted_certificate", message);
        setErrorMsg(message);
        return;
      }
      if (cert.status === "first-use") {
        // Held for explicit confirmation. Pinning something the member never
        // saw would make the later mismatch screen meaningless.
        setPendingTrust({ serverUrl, version: probe.version, cert });
        return;
      }

      await finish(serverUrl, probe.version, cert);
    } catch (err) {
      const message =
        err instanceof ApiError
          ? err.message
          : "Could not reach that Telos node.";
      setError("unreachable", message);
      setErrorMsg(message);
    } finally {
      setLoading(false);
    }
  };

  if (pendingTrust) {
    const { cert } = pendingTrust;
    return (
      <div className="authwrap">
        <div className="authcard">
          <h2 className="connect-title">Trust this node?</h2>
          <p className="connect-hint">
            This is the first connection to {pendingTrust.serverUrl}. Confirm the
            fingerprint matches the one shown by the node&rsquo;s operator.
          </p>
          <dl className="connect-cert">
            {cert.subject && (
              <>
                <dt>Subject</dt>
                <dd>{cert.subject}</dd>
              </>
            )}
            {cert.issuer && (
              <>
                <dt>Issuer</dt>
                <dd>{cert.issuer}</dd>
              </>
            )}
            <dt>SHA-256</dt>
            <dd className="connect-fingerprint">
              {formatFingerprint(cert.fingerprint ?? "")}
            </dd>
          </dl>
          <div className="connect-actions">
            <button
              type="button"
              className="btn rose"
              onClick={() =>
                void finish(pendingTrust.serverUrl, pendingTrust.version, cert)
              }
            >
              Trust and continue
            </button>
            <button
              type="button"
              className="btn"
              onClick={() => setPendingTrust(null)}
            >
              Cancel
            </button>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="authwrap">
      <div className="authcard">
        <div className="lockup">
          <BrandLogo size={42} />
          <span className="word">Connect to Telos</span>
        </div>
        <form onSubmit={handleConnect} className="connect-form">
          <div className="field">
            <label htmlFor="server-url">Server address</label>
            <input
            id="server-url"
            type="text"
            placeholder="https://telos.example.com"
            value={urlInput}
            onChange={(e) => setUrlInput(e.target.value)}
            disabled={loading}
            autoComplete="url"
            autoCapitalize="none"
            spellCheck={false}
            />
          </div>
          <p className="authfeedback error" role="alert">
            {errorMsg}
          </p>
          {/*
            Stated plainly rather than dressed up. Without a native TLS bridge
            the connection is validated by the platform's certificate store and
            nothing is pinned — claiming otherwise on this screen would be the
            lie the pinning module exists to avoid.
          */}
          <p className="connect-hint">
            {certPinningAvailable()
              ? "This node's certificate is pinned on first connection."
              : "Secured by your platform's certificate checks. Certificate pinning needs the desktop app."}
          </p>
          <button type="submit" className="btn rose" disabled={loading}>
            {loading ? "Connecting…" : "Connect"}
          </button>
        </form>
      </div>
    </div>
  );
}
