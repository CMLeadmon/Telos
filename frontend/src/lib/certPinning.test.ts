import { describe, expect, it, afterEach } from "vitest";
import {
  certPinningAvailable,
  normalizeFingerprint,
  verifyCertificate,
  type NativeTlsBridge,
} from "@/lib/certPinning";

const CERT_A =
  "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99";
const CERT_B =
  "11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00";

function installBridge(fingerprint: string | null, subject = "CN=telos.example.com") {
  const bridge: NativeTlsBridge = {
    leafCertificate: async () =>
      fingerprint === null
        ? null
        : { fingerprintSha256: fingerprint, subject, issuer: "CN=Telos Self-Signed" },
  };
  (window as unknown as { __TELOS_NATIVE_TLS__?: NativeTlsBridge }).__TELOS_NATIVE_TLS__ = bridge;
}

afterEach(() => {
  delete (window as unknown as { __TELOS_NATIVE_TLS__?: NativeTlsBridge }).__TELOS_NATIVE_TLS__;
});

describe("verifyCertificate without a native TLS bridge", () => {
  // The load-bearing assertion in this file. A browser cannot see the peer
  // certificate, so the only correct answer is "unsupported" with no
  // fingerprint. Returning a synthesized value here is what made an earlier
  // draft report a match for any certificate presented for the host.
  it("reports unsupported and never synthesizes a fingerprint", async () => {
    expect(certPinningAvailable()).toBe(false);

    const result = await verifyCertificate("https://telos.example.com", null);
    expect(result.status).toBe("unsupported");
    expect(result.fingerprint).toBeNull();
  });

  it("does not claim a match when a pin is already stored", async () => {
    const result = await verifyCertificate("https://telos.example.com", CERT_A);
    expect(result.status).toBe("unsupported");
    expect(result.fingerprint).toBeNull();
  });
});

describe("verifyCertificate with a native TLS bridge", () => {
  it("reports first use when nothing is pinned yet", async () => {
    installBridge(CERT_A);
    const result = await verifyCertificate("https://telos.example.com", null);
    expect(result.status).toBe("first-use");
    expect(result.fingerprint).toBe(normalizeFingerprint(CERT_A));
    expect(result.subject).toBe("CN=telos.example.com");
  });

  it("trusts a certificate whose fingerprint matches the pin", async () => {
    installBridge(CERT_A);
    const result = await verifyCertificate("https://telos.example.com", CERT_A);
    expect(result.status).toBe("trusted");
  });

  // The regression that matters. The rejected implementation derived the
  // fingerprint from the hostname, so the same host always agreed with itself
  // no matter which certificate was presented — pinning that could not detect
  // an interception. Same URL, different certificate, must be a mismatch.
  it("detects a different certificate presented for the same host", async () => {
    installBridge(CERT_A);
    const first = await verifyCertificate("https://telos.example.com", null);
    expect(first.fingerprint).not.toBeNull();

    installBridge(CERT_B);
    const second = await verifyCertificate("https://telos.example.com", first.fingerprint);
    expect(second.status).toBe("mismatch");
    expect(second.fingerprint).toBe(normalizeFingerprint(CERT_B));
  });

  // A false alarm on a security screen teaches people to click through it, so
  // formatting differences must not read as an attack.
  it("compares fingerprints regardless of formatting", async () => {
    installBridge(CERT_A.toLowerCase().replace(/:/g, ""));
    const result = await verifyCertificate("https://telos.example.com", `sha256:${CERT_A}`);
    expect(result.status).toBe("trusted");
  });

  it("reports unsupported for plain HTTP, where there is no certificate", async () => {
    installBridge(CERT_A);
    const result = await verifyCertificate("http://192.168.1.50:8080", CERT_A);
    expect(result.status).toBe("unsupported");
    expect(result.fingerprint).toBeNull();
  });

  it("reports unsupported when the bridge cannot produce a certificate", async () => {
    installBridge(null);
    const result = await verifyCertificate("https://telos.example.com", CERT_A);
    expect(result.status).toBe("unsupported");
  });

  it("reports unsupported rather than trusted when the bridge throws", async () => {
    (window as unknown as { __TELOS_NATIVE_TLS__?: NativeTlsBridge }).__TELOS_NATIVE_TLS__ = {
      leafCertificate: async () => {
        throw new Error("native bridge unavailable");
      },
    };
    const result = await verifyCertificate("https://telos.example.com", CERT_A);
    expect(result.status).toBe("unsupported");
    expect(result.fingerprint).toBeNull();
  });
});
