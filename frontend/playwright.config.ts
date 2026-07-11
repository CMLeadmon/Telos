import { defineConfig } from "@playwright/test";

// No webServer block by convention: run `npm run dev` first, then
// `npx playwright test`.
export default defineConfig({
  testDir: "./e2e",
  use: {
    baseURL: process.env.E2E_BASE_URL ?? "http://localhost:3000",
  },
});
