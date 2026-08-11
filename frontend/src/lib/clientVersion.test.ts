import { describe, expect, it } from "vitest";
import { CLIENT_VERSION, clientBelowMinimum, compareVersions } from "@/lib/clientVersion";

describe("compareVersions", () => {
  it("orders by numeric segment, not lexically", () => {
    // The reason this is not a string compare: "0.10.0" < "0.9.0" lexically.
    expect(compareVersions("0.10.0", "0.9.0")).toBeGreaterThan(0);
    expect(compareVersions("1.0.0", "0.99.99")).toBeGreaterThan(0);
    expect(compareVersions("1.2.3", "1.2.4")).toBeLessThan(0);
  });

  it("treats missing trailing segments as zero", () => {
    expect(compareVersions("1.2", "1.2.0")).toBe(0);
    expect(compareVersions("1.2.1", "1.2")).toBeGreaterThan(0);
  });

  it("tolerates a v prefix and prerelease suffix", () => {
    expect(compareVersions("v1.4.0", "1.4.0")).toBe(0);
    expect(compareVersions("1.4.0-rc1", "1.4.0")).toBe(0);
  });

  it("does not throw on a malformed version", () => {
    expect(compareVersions("", "1.0.0")).toBeLessThan(0);
    expect(compareVersions("not-a-version", "0.0.0")).toBe(0);
  });
});

describe("clientBelowMinimum", () => {
  it("is false when the node states no minimum", () => {
    expect(clientBelowMinimum(null)).toBe(false);
  });

  it("is false when this client meets or exceeds the minimum", () => {
    expect(clientBelowMinimum(CLIENT_VERSION)).toBe(false);
    expect(clientBelowMinimum("0.0.1")).toBe(false);
  });

  // The point of the check: a client the node will not serve must be told so on
  // the connect screen rather than failing in unpredictable ways later.
  it("is true when this client is older than the node requires", () => {
    expect(clientBelowMinimum("999.0.0")).toBe(true);
  });
});
