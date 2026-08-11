import { describe, expect, it, beforeEach, vi } from "vitest";
import { uploadFile } from "@/lib/upload";
import { setServerConfig } from "@/lib/serverConfig";

describe("uploadFile network behavior under auth modes", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    setServerConfig({
      baseUrl: "",
      mode: "cookie",
      accessToken: null,
    });
  });

  it("sets withCredentials=true under cookie auth mode", async () => {
    let mockXhr: {
      open: ReturnType<typeof vi.fn>;
      send: ReturnType<typeof vi.fn>;
      setRequestHeader: ReturnType<typeof vi.fn>;
      withCredentials?: boolean;
      upload: { onprogress: null; onload: null };
      onload: (() => void) | null;
      status: number;
      responseText: string;
    };

    function FakeXHR() {
      mockXhr = {
        open: vi.fn(),
        send: vi.fn().mockImplementation(function (this: typeof mockXhr) {
          if (mockXhr.onload) {
            mockXhr.status = 201;
            mockXhr.responseText = JSON.stringify({
              id: "1",
              filename: "test.png",
              sha256: "abc",
              scan_status: "clean",
            });
            mockXhr.onload();
          }
        }),
        setRequestHeader: vi.fn(),
        upload: { onprogress: null, onload: null },
        onload: null,
        status: 201,
        responseText: "",
      };
      return mockXhr;
    }

    vi.stubGlobal("XMLHttpRequest", FakeXHR);

    const dummyFile = new File(["dummy"], "test.png", { type: "image/png" });
    const promise = uploadFile("/api/v1/users/me/avatar", dummyFile, () => {});

    await expect(promise).resolves.toEqual({
      id: "1",
      filename: "test.png",
      sha256: "abc",
      scan_status: "clean",
    });

    expect(mockXhr!.withCredentials).toBe(true);
    expect(mockXhr!.setRequestHeader).not.toHaveBeenCalledWith("Authorization", expect.any(String));
  });

  it("attaches Authorization header and leaves withCredentials undefined under token auth mode", async () => {
    setServerConfig({
      baseUrl: "https://telos.example.com",
      mode: "token",
      accessToken: "secret-token-xyz",
    });

    let mockXhr: {
      open: ReturnType<typeof vi.fn>;
      send: ReturnType<typeof vi.fn>;
      setRequestHeader: ReturnType<typeof vi.fn>;
      withCredentials?: boolean;
      upload: { onprogress: null; onload: null };
      onload: (() => void) | null;
      status: number;
      responseText: string;
    };

    function FakeXHR() {
      mockXhr = {
        open: vi.fn(),
        send: vi.fn().mockImplementation(function (this: typeof mockXhr) {
          if (mockXhr.onload) {
            mockXhr.status = 201;
            mockXhr.responseText = JSON.stringify({
              id: "2",
              filename: "test.png",
              sha256: "def",
              scan_status: "clean",
            });
            mockXhr.onload();
          }
        }),
        setRequestHeader: vi.fn(),
        upload: { onprogress: null, onload: null },
        onload: null,
        status: 201,
        responseText: "",
      };
      return mockXhr;
    }

    vi.stubGlobal("XMLHttpRequest", FakeXHR);

    const dummyFile = new File(["dummy"], "test.png", { type: "image/png" });
    await uploadFile("/api/v1/users/me/avatar", dummyFile, () => {});

    expect(mockXhr!.withCredentials).toBeUndefined();
    expect(mockXhr!.setRequestHeader).toHaveBeenCalledWith("Authorization", "Bearer secret-token-xyz");
  });

  it("omits credentials and Authorization header in token mode when accessToken is null", async () => {
    setServerConfig({
      baseUrl: "https://telos.example.com",
      mode: "token",
      accessToken: null,
    });

    let mockXhr: {
      open: ReturnType<typeof vi.fn>;
      send: ReturnType<typeof vi.fn>;
      setRequestHeader: ReturnType<typeof vi.fn>;
      withCredentials?: boolean;
      upload: { onprogress: null; onload: null };
      onload: (() => void) | null;
      status: number;
      responseText: string;
    };

    function FakeXHR() {
      mockXhr = {
        open: vi.fn(),
        send: vi.fn().mockImplementation(function (this: typeof mockXhr) {
          if (mockXhr.onload) {
            mockXhr.status = 201;
            mockXhr.responseText = JSON.stringify({
              id: "3",
              filename: "test.png",
              sha256: "ghi",
              scan_status: "clean",
            });
            mockXhr.onload();
          }
        }),
        setRequestHeader: vi.fn(),
        upload: { onprogress: null, onload: null },
        onload: null,
        status: 201,
        responseText: "",
      };
      return mockXhr;
    }

    vi.stubGlobal("XMLHttpRequest", FakeXHR);

    const dummyFile = new File(["dummy"], "test.png", { type: "image/png" });
    await uploadFile("/api/v1/users/me/avatar", dummyFile, () => {});

    expect(mockXhr!.withCredentials).toBeUndefined();
    expect(mockXhr!.setRequestHeader).not.toHaveBeenCalledWith("Authorization", expect.any(String));
  });
});
