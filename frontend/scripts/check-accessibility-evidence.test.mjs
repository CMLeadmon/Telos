import { describe, it } from "node:test";
import assert from "node:assert";
import { verifyAccessibilityEvidence } from "./check-accessibility-evidence.mjs";

describe("check-accessibility-evidence", () => {
  it("verifies accessibility audit markdown file presence and contents", () => {
    assert.strictEqual(verifyAccessibilityEvidence(), true);
  });
});
