import { describe, it } from "node:test";
import assert from "node:assert";
import { verifyInstalledBrowsers } from "./check-installed-browsers.mjs";

describe("check-installed-browsers", () => {
  it("verifies installed browsers configuration without errors", () => {
    assert.strictEqual(verifyInstalledBrowsers(), true);
  });
});
