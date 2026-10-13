import { describe, expect, it, beforeEach, vi } from "vitest";
import { NodeUrlError, getServerConfig, normalizeTrustedNodeUrl, setServerConfig } from "@/lib/serverConfig";

describe("serverConfig", () => {
  beforeEach(() => {
    setServerConfig({
      baseUrl: "",
      mode: "cookie",
      accessToken: null,
    });
  });

  it("returns default configuration initially", () => {
    const config = getServerConfig();
    expect(config.baseUrl).toBe("");
    expect(config.mode).toBe("cookie");
    expect(config.accessToken).toBeNull();
  });

  it("updates configuration when setServerConfig is called", () => {
    setServerConfig({
      baseUrl: "https://telos.example.com",
      mode: "token",
      accessToken: "test-access-token",
    });

    const config = getServerConfig();
    expect(config.baseUrl).toBe("https://telos.example.com");
    expect(config.mode).toBe("token");
    expect(config.accessToken).toBe("test-access-token");
  });
});

describe("serverConfig base URL normalization", () => {
  // apiBase() concatenates baseUrl with a leading-slash path, so a stored
  // trailing slash produces https://host//api/v1/... on every single request.
  it("strips trailing slashes so callers can always append a path", () => {
    setServerConfig({ baseUrl: "https://telos.example.com/", mode: "cookie", accessToken: null });
    expect(getServerConfig().baseUrl).toBe("https://telos.example.com");

    setServerConfig({ baseUrl: "https://telos.example.com/telos//", mode: "cookie", accessToken: null });
    expect(getServerConfig().baseUrl).toBe("https://telos.example.com/telos");
  });

  it("keeps an explicit port and path prefix", () => {
    setServerConfig({ baseUrl: "http://192.168.1.50:8080/telos", mode: "cookie", accessToken: null });
    expect(getServerConfig().baseUrl).toBe("http://192.168.1.50:8080/telos");
  });

  // Rejected at the boundary rather than at each use: an address with no scheme
  // is a relative path to apiBase() but throws inside wsBase().
  it("rejects an address that is not an absolute http(s) URL", () => {
    for (const bad of ["telos.example.com", "/telos", "ftp://telos.example.com", "javascript:alert(1)"]) {
      expect(() =>
        setServerConfig({ baseUrl: bad, mode: "cookie", accessToken: null }),
      ).toThrow();
    }
  });

  it("does not hand out the live configuration object", () => {
    setServerConfig({ baseUrl: "https://telos.example.com", mode: "cookie", accessToken: null });
    getServerConfig().baseUrl = "https://attacker.example.com";
    expect(getServerConfig().baseUrl).toBe("https://telos.example.com");
  });
});

describe("normalizeTrustedNodeUrl", () => {
  it("adds https to a bare host and folds case and the default port", () => {
    expect(normalizeTrustedNodeUrl("telos.example.com")).toBe("https://telos.example.com");
    expect(normalizeTrustedNodeUrl("  Telos.Example.COM:443/ ")).toBe("https://telos.example.com");
    expect(normalizeTrustedNodeUrl("https://telos.example.com:8443")).toBe("https://telos.example.com:8443");
  });

  it("refuses plain http except loopback in a debug build", () => {
    expect(() => normalizeTrustedNodeUrl("http://telos.example.com", false)).toThrow(NodeUrlError);
    expect(() => normalizeTrustedNodeUrl("http://telos.example.com", true)).toThrow(/https/i);
    expect(() => normalizeTrustedNodeUrl("http://localhost:8080", false)).toThrow(/https/i);
    expect(normalizeTrustedNodeUrl("http://localhost:8080", true)).toBe("http://localhost:8080");
    expect(normalizeTrustedNodeUrl("http://127.0.0.1:8080", true)).toBe("http://127.0.0.1:8080");
    expect(normalizeTrustedNodeUrl("http://[::1]:8080", true)).toBe("http://[::1]:8080");
    expect(() => normalizeTrustedNodeUrl("http://localhost.evil.example", true)).toThrow(/https/i);
  });

  it("distinguishes invalid, credentialed and path-bearing addresses", () => {
    const problem = (raw: string) => {
      try {
        normalizeTrustedNodeUrl(raw, false);
      } catch (error) {
        return (error as NodeUrlError).problem;
      }
      return null;
    };
    expect(problem("http://")).toBe("invalid_url");
    expect(problem("https://user:pw@telos.example.com")).toBe("credentials");
    expect(problem("https://telos.example.com/telos")).toBe("not_an_origin");
    expect(problem("https://telos.example.com/?a=1")).toBe("not_an_origin");
    expect(problem("https://telos.example.com/#x")).toBe("not_an_origin");
    expect(problem("ftp://telos.example.com")).toBe("https_required");
    expect(problem("")).toBe("invalid_url");
  });

  it("is a release-build refusal: the loopback exception follows NODE_ENV", async () => {
    const previous = process.env.NODE_ENV;
    try {
      vi.stubEnv("NODE_ENV", "production");
      expect(() => normalizeTrustedNodeUrl("http://localhost:8080")).toThrow(/https/i);
      vi.stubEnv("NODE_ENV", "development");
      expect(normalizeTrustedNodeUrl("http://localhost:8080")).toBe("http://localhost:8080");
    } finally {
      vi.unstubAllEnvs();
      expect(process.env.NODE_ENV).toBe(previous);
    }
  });
});
