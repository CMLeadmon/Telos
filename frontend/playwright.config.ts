import { defineConfig, devices } from "@playwright/test";

// Two modes.
//
// Development (default): no webServer block by convention -- run `npm run dev` first, then
// `npx playwright test`. Projects are the Chromium desktop and the two mobile emulations.
//
// Candidate (TELOS_CANDIDATE_BROWSERS=1): run the hosted-node journeys against a deployed candidate
// (E2E_BASE_URL=https://..., owner/member credentials in the environment) in the four automated
// browser platforms of the browser matrix. Project names are the matrix platform ids because
// scripts/lib/browser_report.py maps results by project name. Nothing is mocked; trace, video and
// screenshots stay off because the journeys carry credentials; the JSON report is for the report
// tool only and is never published (it can hold URLs and headers).
//   Chrome and Edge need the vendor channels installed (`npx playwright install chrome msedge`).
const candidate = process.env.TELOS_CANDIDATE_BROWSERS === "1";

export default defineConfig({
  testDir: "./e2e",
  ...(candidate
    ? {
        testMatch: ["hosted-node.spec.ts"],
        reporter: [["json", { outputFile: process.env.PLAYWRIGHT_JSON_OUTPUT_NAME ?? "playwright-candidate.json" }], ["list"]] as never,
        retries: 0,
        fullyParallel: false,
      }
    : {}),
  use: {
    baseURL: process.env.E2E_BASE_URL ?? "http://localhost:3000",
    ...(candidate ? { trace: "off" as const, video: "off" as const, screenshot: "off" as const } : {}),
  },
  projects: candidate
    ? [
        { name: "chrome", use: { ...devices["Desktop Chrome"], channel: "chrome" } },
        { name: "firefox", use: { ...devices["Desktop Firefox"] } },
        { name: "edge", use: { ...devices["Desktop Edge"], channel: "msedge" } },
        { name: "webkit", use: { ...devices["Desktop Safari"] } },
      ]
    : [
        { name: "chromium", use: { ...devices["Desktop Chrome"] } },
        { name: "mobile-chrome", use: { ...devices["Pixel 7"] } },
        { name: "mobile-safari", use: { ...devices["iPhone 13"] } },
      ],
});
