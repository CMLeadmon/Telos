import { describe, expect, it, beforeEach } from "vitest";
import { assetUrl } from "@/lib/api";
import { setServerConfig } from "@/lib/serverConfig";

describe("assetUrl helper", () => {
  beforeEach(() => {
    setServerConfig({
      baseUrl: "",
      mode: "cookie",
      accessToken: null,
    });
  });

  it("returns empty string when path is undefined or empty", () => {
    expect(assetUrl(undefined)).toBe("");
    expect(assetUrl("")).toBe("");
  });

  it("returns unmodified path when path is absolute URL or blob", () => {
    expect(assetUrl("https://images.example.com/cover.jpg")).toBe("https://images.example.com/cover.jpg");
    expect(assetUrl("http://images.example.com/cover.jpg")).toBe("http://images.example.com/cover.jpg");
    expect(assetUrl("blob:http://localhost/uuid")).toBe("blob:http://localhost/uuid");
  });

  it("returns relative path unchanged when baseUrl is empty", () => {
    expect(assetUrl("/api/v1/media/items/123/cover")).toBe("/api/v1/media/items/123/cover");
  });

  it("prepends baseUrl to relative path when configured", () => {
    setServerConfig({
      baseUrl: "https://telos.example.com",
      mode: "token",
      accessToken: "token-123",
    });
    expect(assetUrl("/api/v1/media/items/123/cover")).toBe("https://telos.example.com/api/v1/media/items/123/cover");
    expect(assetUrl("api/v1/media/items/123/cover")).toBe("https://telos.example.com/api/v1/media/items/123/cover");
  });
});
