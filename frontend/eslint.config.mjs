import { defineConfig, globalIgnores } from "eslint/config";
import nextVitals from "eslint-config-next/core-web-vitals";
import nextTs from "eslint-config-next/typescript";

export default defineConfig([
  ...nextVitals,
  ...nextTs,
  // src-tauri/target holds the desktop build's generated assets (including minified JavaScript); it is output, not source.
  globalIgnores([".next/**", "out/**", "node_modules/**", "next-env.d.ts", "src-tauri/target/**"]),
]);
