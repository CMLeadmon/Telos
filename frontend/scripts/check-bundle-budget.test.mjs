import { describe, it } from "node:test";
import assert from "node:assert";
import { verifyBundleBudget } from "./check-bundle-budget.mjs";

describe("check-bundle-budget", () => {
  it("runs bundle budget verification function without errors", () => {
    assert.strictEqual(verifyBundleBudget(), true);
  });
});
