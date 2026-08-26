import assert from "node:assert";
import { spawnSync } from "node:child_process";
import { describe, it } from "node:test";
import { fileURLToPath } from "node:url";

describe("check-accessibility-evidence", () => {
  it("reports not_run until a candidate-run WCAG audit exists", () => {
    const result = spawnSync(
      process.execPath,
      [fileURLToPath(new URL("./check-accessibility-evidence.mjs", import.meta.url))],
      { encoding: "utf8" },
    );

    assert.strictEqual(result.status, 3);
    assert.strictEqual(result.stdout, "");
    assert.strictEqual(
      result.stderr,
      "not_run: candidate-run WCAG 2.2 AA audit evidence is required\n",
    );
  });
});
