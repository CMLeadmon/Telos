import { describe, expect, it } from "vitest";
import fs from "node:fs";
import path from "node:path";

// The frontend and the Tauri shell agree on window globals, and both sides
// fall back silently when they are absent — that silence is what makes the same
// static export work in a browser. It also means a rename on either side turns
// off credential persistence or the native shell helpers with no error anywhere.
// Nothing at runtime can catch that, so the contract is checked here, across the
// language boundary, in both directions.

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
      ["src/lib/secureStorage.ts", "__TELOS_NATIVE_CREDENTIALS__"],
      ["src/lib/platform.ts", "__TELOS_NATIVE_SHELL__"],
    ] as const) {
      expect(read(module)).toContain(global);
      expect(shell).toContain(`window.${global}`);
    }
  });

  it("implements every method the frontend calls on those bridges", () => {
    const shell = shellSource();
    for (const method of [
      "openExternal", // NativeShellBridge
      "writeClipboard",
      "get:", // NativeCredentialBridge
      "set:",
      "delete:",
      "legacyStoreRetired",
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

describe("retired plaintext secret store", () => {
  const walk = (dir: string, out: string[] = []): string[] => {
    for (const entry of fs.readdirSync(path.join(root, dir), { withFileTypes: true })) {
      const rel = `${dir}/${entry.name}`;
      if (entry.isDirectory()) walk(rel, out);
      else out.push(rel);
    }
    return out;
  };

  // The shell's secrets moved to the OS credential service. None of the old
  // bridge names, key prefix or plugin may remain in code or configuration. The
  // legacy filename is allowed only in the one-time retirement helper.
  it("leaves no store plugin, bridge or key prefix in production sources", () => {
    const files = [
      ...walk("src").filter((f) => /\.(ts|tsx)$/.test(f) && !/\.test\./.test(f)),
      ...walk("src-tauri/src").filter((f) => f.endsWith(".rs")),
      "src-tauri/Cargo.toml",
      ...walk("src-tauri/capabilities"),
    ];
    for (const file of files) {
      let text = read(file);
      const testMod = text.indexOf("#[cfg(test)]");
      if (file.endsWith(".rs") && testMod !== -1) text = text.slice(0, testMod);
      for (const forbidden of ["tauri_plugin_store", "tauri-plugin-store", "telos_secret_", "__TELOS_NATIVE_STORE__", "store:default"]) {
        expect(text, `${file} still mentions ${forbidden}`).not.toContain(forbidden);
      }
    }
  });

  it("names the legacy file only in the retirement helper", () => {
    const offenders = walk("src-tauri/src")
      .filter((f) => f.endsWith(".rs"))
      .filter((f) => read(f).includes("telos-secrets.json"));
    expect(offenders).toEqual(["src-tauri/src/credentials.rs"]);
    for (const file of walk("src").filter((f) => /\.(ts|tsx)$/.test(f) && !/\.test\./.test(f))) {
      expect(read(file), file).not.toContain("telos-secrets.json");
    }
  });

  it("pins keyring 4.1.6 with the v1 feature and no cli feature", () => {
    const manifest = read("src-tauri/Cargo.toml");
    expect(manifest).toMatch(/keyring\s*=\s*\{[^}]*version\s*=\s*"=4\.1\.6"[^}]*\}/);
    const line = /keyring\s*=\s*\{[^}]*\}/.exec(manifest)![0];
    expect(line).toContain('features = ["v1"]');
    expect(line).toContain("default-features = false");
    expect(line).not.toContain("cli");
  });
});
