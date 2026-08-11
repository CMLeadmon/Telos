import { describe, expect, it, beforeEach } from "vitest";
import { apiBase, wsBase } from "@/lib/api";
import { setServerConfig } from "@/lib/serverConfig";

describe("apiBase and wsBase parameterization", () => {
  beforeEach(() => {
    setServerConfig({
      baseUrl: "",
      mode: "cookie",
      accessToken: null,
    });
  });

  it("apiBase returns empty string when baseUrl is unconfigured", () => {
    expect(apiBase()).toBe("");
  });

  it("apiBase returns configured baseUrl when set", () => {
    setServerConfig({
      baseUrl: "https://telos.example.com",
      mode: "cookie",
      accessToken: null,
    });
    expect(apiBase()).toBe("https://telos.example.com");
  });

  it("wsBase derives wss URL from https baseUrl", () => {
    setServerConfig({
      baseUrl: "https://telos.example.com",
      mode: "cookie",
      accessToken: null,
    });
    expect(wsBase()).toBe("wss://telos.example.com");
  });

  it("wsBase derives ws URL from http baseUrl", () => {
    setServerConfig({
      baseUrl: "http://192.168.1.50:8080",
      mode: "cookie",
      accessToken: null,
    });
    expect(wsBase()).toBe("ws://192.168.1.50:8080");
  });
});

describe("wsBase path prefix", () => {
  // apiBase() returns baseUrl whole, so dropping the prefix here would send the
  // socket somewhere other than every HTTP call on the same node.
  it("preserves a path prefix so the socket follows the HTTP calls", () => {
    setServerConfig({
      baseUrl: "https://telos.example.com/telos",
      mode: "cookie",
      accessToken: null,
    });
    expect(wsBase()).toBe("wss://telos.example.com/telos");
    expect(`${apiBase()}/api/v1/chat/ws`).toBe("https://telos.example.com/telos/api/v1/chat/ws");
  });
});
