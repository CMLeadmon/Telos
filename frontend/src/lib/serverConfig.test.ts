import { describe, expect, it, beforeEach } from "vitest";
import { getServerConfig, setServerConfig } from "@/lib/serverConfig";

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
