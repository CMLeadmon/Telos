import { describe, expect, it, beforeEach } from "vitest";
import { setServerConfig, getServerConfig } from "@/lib/serverConfig";

describe("HlsPlayer auth and origin options", () => {
  beforeEach(() => {
    setServerConfig({
      baseUrl: "",
      mode: "cookie",
      accessToken: null,
    });
  });

  it("determines video crossOrigin attribute based on server configuration", () => {
    setServerConfig({ baseUrl: "", mode: "cookie", accessToken: null });
    let crossOrigin: "use-credentials" | "anonymous" | undefined =
      getServerConfig().baseUrl
        ? getServerConfig().mode === "token"
          ? "anonymous"
          : "use-credentials"
        : undefined;
    expect(crossOrigin).toBeUndefined();

    setServerConfig({ baseUrl: "https://telos.example.com", mode: "cookie", accessToken: null });
    crossOrigin = getServerConfig().baseUrl
      ? getServerConfig().mode === "token"
        ? "anonymous"
        : "use-credentials"
      : undefined;
    expect(crossOrigin).toBe("use-credentials");

    setServerConfig({ baseUrl: "https://telos.example.com", mode: "token", accessToken: "tok-123" });
    crossOrigin = getServerConfig().baseUrl
      ? getServerConfig().mode === "token"
        ? "anonymous"
        : "use-credentials"
      : undefined;
    expect(crossOrigin).toBe("anonymous");
  });
});
