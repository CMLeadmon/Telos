/**
 * visual-shoot — capture app pages and Design System mockups at matching
 * viewports so they can be compared side by side.
 *
 * The DS mockups are fixed 1440x900 posters (`.app` has a hardcoded size), so
 * mockups are captured as an element screenshot of `.app` while app pages are
 * captured at a 1440x900 viewport. That makes the two directly comparable
 * without scaling either one.
 *
 *   node scripts/visual-shoot.mjs app     # authenticated app pages
 *   node scripts/visual-shoot.mjs mockup  # DS mockups (needs the static server)
 *   node scripts/visual-shoot.mjs both
 */
import { chromium, devices } from "@playwright/test";
import { mkdir } from "node:fs/promises";
import path from "node:path";

const OUT = process.env.SHOT_DIR ?? "/tmp/claude-1000/-var-home-cleadmon-Projects-Telos/bf4da254-ec31-48d9-96e9-938bbe968925/scratchpad/shots";
const APP = process.env.APP_URL ?? "http://localhost:3000";
const DS = process.env.DS_URL ?? "http://127.0.0.1:8900";
const USER = process.env.E2E_USER ?? "TheMightyDuckli";
const PASS = process.env.E2E_PASS ?? "MartinD1";

const DESKTOP = { width: 1440, height: 900 };
const PHONE = devices["Pixel 7"].viewport;

// Theme is a zustand persist store in localStorage; seeding it before first
// paint avoids a flash of the default theme in the screenshot.
const seedTheme = (theme) => ({
  name: "telos-theme",
  value: JSON.stringify({ state: { theme }, version: 0 }),
});

const APP_PAGES = [
  { name: "chat", url: "/chat/" },
  { name: "stream", url: "/stream/" },
  { name: "library", url: "/library/" },
  { name: "files", url: "/files/" },
  { name: "settings", url: "/settings/" },
];

const MOCKUPS = [
  "chat-ink",
  "chat-synthwave",
  "chat-ink-mobile",
  "library-ink",
  "library-synthwave",
  "stream-synthwave",
  "stream-ink-mobile",
  "files-synthwave",
  "landing-ink",
  "landing-synthwave",
];

// Landing and login are public, so they are captured without the login step.
const PUBLIC_PAGES = [
  { name: "landing", url: "/" },
  { name: "login", url: "/login/" },
];

async function shootPublic(browser) {
  for (const theme of ["ink", "synthwave"]) {
    for (const [label, viewport] of [["desktop", DESKTOP], ["mobile", PHONE]]) {
      const ctx = await browser.newContext({ viewport });
      await ctx.addInitScript((seed) => {
        window.localStorage.setItem(seed.name, seed.value);
      }, seedTheme(theme));
      const page = await ctx.newPage();
      for (const p of PUBLIC_PAGES) {
        await page.goto(`${APP}${p.url}`, { waitUntil: "networkidle", timeout: 20000 })
          .catch(() => page.goto(`${APP}${p.url}`, { waitUntil: "domcontentloaded" }));
        await page.waitForTimeout(700);
        const file = path.join(OUT, "app", `${p.name}-${theme}-${label}.png`);
        await page.screenshot({ path: file });
        console.log("public  ", path.basename(file));
      }
      await ctx.close();
    }
  }
}

async function shootApp(browser) {
  for (const theme of ["ink", "synthwave"]) {
    for (const [label, viewport] of [["desktop", DESKTOP], ["mobile", PHONE]]) {
      const ctx = await browser.newContext({ viewport });
      await ctx.addInitScript((seed) => {
        window.localStorage.setItem(seed.name, seed.value);
      }, seedTheme(theme));

      // The seeded theme survives only until login: usePreferencesStore.load()
      // spreads the server response last, so a saved server-side theme wins.
      // Override it in flight so the capture reflects the theme we asked for
      // without mutating the account's real preferences.
      await ctx.route("**/api/v1/users/me/preferences", async (route) => {
        if (route.request().method() !== "GET") return route.continue();
        const res = await route.fetch();
        let body = {};
        try {
          body = await res.json();
        } catch {
          body = {};
        }
        await route.fulfill({ response: res, json: { ...body, theme } });
      });

      const page = await ctx.newPage();

      // Log in once per context; the session cookie carries the rest.
      await page.goto(`${APP}/login/`, { waitUntil: "domcontentloaded" });
      await page.getByLabel("username").fill(USER);
      await page.getByLabel("password", { exact: true }).fill(PASS);
      await page.getByRole("button", { name: /enter the node/i }).click();
      await page.waitForURL((u) => !u.pathname.startsWith("/login"), { timeout: 15000 })
        .catch(() => {});

      for (const p of APP_PAGES) {
        try {
          await page.goto(`${APP}${p.url}`, { waitUntil: "networkidle", timeout: 20000 });
        } catch {
          await page.goto(`${APP}${p.url}`, { waitUntil: "domcontentloaded" });
        }
        await page.waitForTimeout(900);
        const file = path.join(OUT, "app", `${p.name}-${theme}-${label}.png`);
        await page.screenshot({ path: file });
        console.log("app     ", path.basename(file));
      }
      await ctx.close();
    }
  }
}

async function shootMockups(browser) {
  const ctx = await browser.newContext({ viewport: { width: 1600, height: 1000 } });
  const page = await ctx.newPage();
  for (const m of MOCKUPS) {
    const url = `${DS}/mockups/${m}.html`;
    const res = await page.goto(url, { waitUntil: "networkidle", timeout: 20000 }).catch(() => null);
    if (!res || !res.ok()) {
      console.log("mockup   SKIP (not fetched yet):", m);
      continue;
    }
    await page.waitForTimeout(700); // let lucide inject icons
    const app = page.locator(".app").first();
    const file = path.join(OUT, "mockup", `${m}.png`);
    if (await app.count()) {
      await app.screenshot({ path: file });
    } else {
      await page.screenshot({ path: file, fullPage: true });
    }
    console.log("mockup  ", path.basename(file));
  }
  await ctx.close();
}

const mode = process.argv[2] ?? "both";
await mkdir(path.join(OUT, "app"), { recursive: true });
await mkdir(path.join(OUT, "mockup"), { recursive: true });

const browser = await chromium.launch();
if (mode === "public") await shootPublic(browser);
if (mode === "app" || mode === "both") await shootApp(browser);
if (mode === "both") await shootPublic(browser);
if (mode === "mockup" || mode === "both") await shootMockups(browser);
await browser.close();
console.log("\nshots in", OUT);
