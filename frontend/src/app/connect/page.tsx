"use client";

import React, { useState } from "react";
import { useRouter } from "next/navigation";
import { BrandLogo } from "@/components/BrandLogo";
import { useConnectionStore } from "@/stores/useConnectionStore";
import { ApiError, probeNode } from "@/lib/api";
import { NodeUrlError, normalizeTrustedNodeUrl } from "@/lib/serverConfig";
import { CLIENT_VERSION, clientBelowMinimum, compareVersions } from "@/lib/clientVersion";

const UNREACHABLE_GUIDANCE =
  "Could not establish a trusted connection to that Telos node. Check the address, and make sure its HTTPS certificate is trusted by this computer (a private CA must be installed in the operating system's trust store).";

export default function ConnectPage() {
  const router = useRouter();
  const [urlInput, setUrlInput] = useState("");
  const [loading, setLoading] = useState(false);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const setValidating = useConnectionStore((s) => s.setValidating);
  const setConfigured = useConnectionStore((s) => s.setConfigured);
  const setError = useConnectionStore((s) => s.setError);
  const setVersionSkewSoft = useConnectionStore((s) => s.setVersionSkewSoft);

  const handleConnect = async (e: React.FormEvent) => {
    e.preventDefault();
    setErrorMsg(null);
    if (!urlInput.trim()) return;

    // A bare host is what people type; it gains https://, never http://. Only
    // platform-trusted HTTPS is accepted (plain HTTP loopback exists in debug
    // builds alone), and there is no certificate to confirm or override: the
    // operating system either trusts the node's certificate or the connection
    // fails.
    let serverUrl: string;
    try {
      serverUrl = normalizeTrustedNodeUrl(urlInput);
    } catch (err) {
      if (err instanceof NodeUrlError) {
        setError(err.problem === "https_required" ? "https_required" : "invalid_address", err.message);
        setErrorMsg(err.message);
      } else {
        setErrorMsg("That is not a valid server address.");
      }
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

      setConfigured(probe.version ?? undefined);
      // Informational only: a node ahead of this client may expose things it
      // cannot render. A client behind the node's stated minimum was already
      // rejected above.
      setVersionSkewSoft(!!probe.version && compareVersions(probe.version, CLIENT_VERSION) > 0);
      router.push("/login/");
    } catch (err) {
      // A WebView fetch hides whether a rejection was TLS or transport, so one
      // honest message covers both rather than asserting a diagnosis the API
      // cannot provide. A non-Telos answer carries its own message.
      const message = err instanceof ApiError && err.status !== 0 ? err.message : UNREACHABLE_GUIDANCE;
      setError("unreachable", message);
      setErrorMsg(message);
    } finally {
      setLoading(false);
    }
  };

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
          <p className="connect-hint">
            Telos connects over HTTPS only, and your computer decides whether to trust the node&rsquo;s certificate.
          </p>
          <button type="submit" className="btn rose" disabled={loading}>
            {loading ? "Connecting…" : "Connect"}
          </button>
        </form>
      </div>
    </div>
  );
}
