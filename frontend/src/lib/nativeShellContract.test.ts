import { describe, expect, it } from "vitest";
import fs from "node:fs";
import path from "node:path";

// The frontend and the Tauri shell agree on two window globals, and both sides
// fall back silently when they are absent — that silence is what makes the same
// static export work in a browser. It also means a rename on either side turns
// off certificate pinning and credential persistence with no error anywhere: the
// client just quietly forgets its refresh token every launch and treats every
// certificate as first-use. Nothing at runtime can catch that, so the contract
// is checked here, across the language boundary, in both directions.

const root = path.resolve(__dirname, "../..");
const read = (rel: string) => fs.readFileSync(path.join(root, rel), "utf-8");

const SHELL = "src-tauri/src/lib.rs";

/**
 * lib.rs ships its own #[cfg(test)] module asserting these same strings, and
 * reading the file whole made every check here pass on the strength of that
 * module rather than the code — renaming the real global still left the literal
 * sitting in the Rust test's assertion. Only production Rust counts.
 */
function shellSource(): string {
  const src = read(SHELL);
  const testMod = src.indexOf("#[cfg(test)]");
  return testMod === -1 ? src : src.slice(0, testMod);
}

describe("native shell bridge contract", () => {
  it("publishes every global the frontend reads", () => {
    const shell = shellSource();
    // Read from the modules that consume them, not from a literal here — a
    // constant duplicated in the test would drift with the shell, not with the
    // code that actually looks the global up.
    for (const [module, global] of [
      ["src/lib/certPinning.ts", "__TELOS_NATIVE_TLS__"],
      ["src/lib/secureStorage.ts", "__TELOS_NATIVE_STORE__"],
      ["src/lib/platform.ts", "__TELOS_NATIVE_SHELL__"],
    ] as const) {
      expect(read(module)).toContain(global);
      expect(shell).toContain(`window.${global}`);
    }
  });

  it("implements every method the frontend calls on those bridges", () => {
    const shell = shellSource();
    for (const method of [
      "leafCertificate", // NativeTlsBridge
      "openExternal", // NativeShellBridge
      "writeClipboard",
      "get:", // NativeSecretStore
      "set:",
      "delete:",
    ]) {
      expect(shell).toContain(method);
    }
  });

  it("registers every command the injected script invokes", () => {
    const shell = shellSource();
    const invoked = [...shell.matchAll(/invoke\("([a-z_]+)"/g)].map((m) => m[1]);
    expect(invoked.length).toBeGreaterThan(0);
    const handler = shell.slice(shell.indexOf("generate_handler!"));
    for (const command of invoked) {
      expect(handler).toContain(command);
    }
  });

  it("keeps the Tauri config aligned with the frontend build output", () => {
    const conf = JSON.parse(read("src-tauri/tauri.conf.json"));
    expect(conf.identifier).toBe("com.telos.app");
    // next.config.ts exports to out/; pointing the bundle elsewhere ships an
    // empty or stale app rather than failing.
    expect(conf.build.frontendDist).toBe("../out");
  });

  it("commits every icon the build opens", () => {
    const conf = JSON.parse(read("src-tauri/tauri.conf.json"));
    // tauri::generate_context! opens each of these at compile time, so a
    // missing one is a build failure, not a cosmetic gap.
    for (const icon of conf.bundle.icon as string[]) {
      expect(fs.existsSync(path.join(root, "src-tauri", icon))).toBe(true);
    }
  });
});
