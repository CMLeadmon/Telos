# LiveKit TURN Server Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enable LiveKit's embedded TURN server (TLS terminated by Traefik) so WebRTC voice media has a relay fallback for remote and UDP-blocked clients.

**Architecture:** Config-only change — no new container, no application code. LiveKit's built-in TURN server is enabled in `config/livekit.yaml`; it auto-issues TURN credentials inside the access token the gateway already mints, so the frontend is untouched. Traefik terminates TLS on a new `:5349` entrypoint (reusing its existing default cert store) and forwards plain TCP to LiveKit's TURN running `external_tls: true`. Plain TURN/UDP on `:3478` serves non-UDP-blocked clients.

**Tech Stack:** LiveKit v1.10 (`/livekit-server`), Traefik v3.3, podman-compose, self-signed dev cert / Let's Encrypt prod cert.

**Spec:** `docs/superpowers/specs/2026-07-13-livekit-turn-server-design.md`

## Global Constraints

- Go is NOT installed on the host; podman (not docker) is the runtime. Compose command is `podman-compose`.
- No application code changes — this is infrastructure/config only. Verification is config-parse + log + behavior, not unit tests.
- No hardcoded domain: the TURN domain comes from `TELOS_DOMAIN` (already in `.env.example`) via `LIVEKIT_TURN_DOMAIN` (a real LiveKit env override, confirmed via `livekit-server help-verbose`). Compose interpolates `${TELOS_DOMAIN}` from `.env`.
- No hardcoded secrets; all credentials via `.env` interpolation.
- Traefik reaches LiveKit over the `telos-ingress` network (LiveKit is already a member).
- Commit after every task.

## Grounding facts (verified against the running stack)

- `/livekit-server` supports per-key env overrides: `LIVEKIT_TURN_ENABLED`, `LIVEKIT_TURN_DOMAIN`, `LIVEKIT_TURN_TLS_PORT`, `LIVEKIT_TURN_UDP_PORT`, `LIVEKIT_TURN_EXTERNAL_TLS` (from `livekit-server help-verbose`). Env/flags override the `--config` file per-key.
- Traefik cert wiring: `config/dynamic/certificates.yaml` defines a `default` TLS store. A TCP router with `tls: {}` (termination) reuses that default cert.
- Current livekit compose env block:
  ```yaml
      environment:
        - "LIVEKIT_KEYS=${LIVEKIT_API_KEY}: ${LIVEKIT_API_SECRET}"
        - REDIS_PASSWORD=${REDIS_PASSWORD}
  ```
- Current livekit published ports: `7880:7880`, `7881:7881`, `3478:3478/udp`, `50000-50100:50000-50100/udp`.
- Current traefik entrypoints (`config/traefik.yaml`): `web` (:80 → redirect), `websecure` (:443). Traefik published ports: `80:80`, `443:443`.

## File Structure

- Modify: `config/livekit.yaml` — add the static `turn:` block (no domain)
- Modify: `docker-compose.yml` — livekit `LIVEKIT_TURN_DOMAIN` env; traefik `turn` entrypoint port `5349:5349`
- Modify: `config/traefik.yaml` — add `turn` entrypoint on `:5349`
- Create: `config/dynamic/turn.yaml` — TCP router (TLS termination) + TCP service → `livekit:5349`
- Modify: `.env.example` — document `TELOS_DOMAIN` already drives TURN (comment only)
- Modify: `README` / spec note — firewall port requirements (documentation)

---

### Task 1: Enable LiveKit embedded TURN (static config + domain env)

**Files:**
- Modify: `config/livekit.yaml`
- Modify: `docker-compose.yml` (livekit `environment:`)

**Interfaces:**
- Produces: LiveKit listens for TURN/UDP on `3478` and plain TCP (external TLS) on `5349`; `turn.domain` resolves from `TELOS_DOMAIN`. Later tasks (Traefik router) forward TLS to `livekit:5349`.

- [ ] **Step 1: Add the TURN block to `config/livekit.yaml`**

Current file:
```yaml
port: 7880
rtc:
  tcp_port: 7881
  port_range_start: 50000
  port_range_end: 50100
  use_external_ip: true
redis:
  address: redis:6379
```

Replace with (domain intentionally omitted — supplied via env so nothing is hardcoded):
```yaml
port: 7880
rtc:
  tcp_port: 7881
  port_range_start: 50000
  port_range_end: 50100
  use_external_ip: true
turn:
  enabled: true
  tls_port: 5349
  udp_port: 3478
  external_tls: true
redis:
  address: redis:6379
```

- [ ] **Step 2: Add the domain env override to the livekit service in `docker-compose.yml`**

Find the livekit `environment:` block:
```yaml
    environment:
      - "LIVEKIT_KEYS=${LIVEKIT_API_KEY}: ${LIVEKIT_API_SECRET}"
      - REDIS_PASSWORD=${REDIS_PASSWORD}
```
Change to:
```yaml
    environment:
      - "LIVEKIT_KEYS=${LIVEKIT_API_KEY}: ${LIVEKIT_API_SECRET}"
      - REDIS_PASSWORD=${REDIS_PASSWORD}
      - "LIVEKIT_TURN_DOMAIN=${TELOS_DOMAIN}"
```

- [ ] **Step 3: Validate compose still parses**

Run (repo root): `podman-compose config >/dev/null && echo OK`
Expected: `OK` (no interpolation or YAML errors).

- [ ] **Step 4: Recreate livekit and confirm TURN is enabled**

> NOTE FOR EXECUTOR: recreating the live `telos-livekit` service is a production-affecting action. In auto/sandboxed mode this may be denied — if so, STOP and ask the user to run it (`! podman-compose up -d livekit`) or grant permission. Do not work around the denial.

Run: `podman-compose up -d livekit`
Then: `podman exec telos-livekit /livekit-server --config /etc/livekit/config.yaml ports 2>&1 | grep -i turn` — or inspect logs:
`podman logs --tail 40 telos-livekit 2>&1 | grep -iE "turn|5349|3478"`
Expected: log lines showing the TURN server starting / listening on 5349 (TLS/external) and 3478 (UDP), and the domain equal to the value of `TELOS_DOMAIN`. No bind errors.

- [ ] **Step 5: Commit**

```bash
git add config/livekit.yaml docker-compose.yml
git commit -m "feat(voice): enable LiveKit embedded TURN (udp 3478, external-tls 5349)"
```

---

### Task 2: Traefik TLS entrypoint + TCP router for TURN

**Files:**
- Modify: `config/traefik.yaml` (add `turn` entrypoint)
- Create: `config/dynamic/turn.yaml` (TCP router + service)
- Modify: `docker-compose.yml` (publish `5349:5349` on traefik)

**Interfaces:**
- Consumes: LiveKit TURN plain-TCP listener on `livekit:5349` (Task 1).
- Produces: `turns://<TELOS_DOMAIN>:5349` terminates at Traefik (default-store cert) and forwards to LiveKit. Firewall/docs task references port 5349.

- [ ] **Step 1: Add the `turn` entrypoint to `config/traefik.yaml`**

Current `entryPoints`:
```yaml
entryPoints:
  web:
    address: ":80"
    http:
      redirections:
        entryPoint:
          to: websecure
          scheme: https
  websecure:
    address: ":443"
```
Add the `turn` entrypoint after `websecure`:
```yaml
  turn:
    address: ":5349"
```

- [ ] **Step 2: Create `config/dynamic/turn.yaml`**

TCP router with TLS termination (reuses the `default` store cert from `certificates.yaml`). `HostSNI(\`*\`)` is valid for TLS *termination* (not passthrough) and keeps the router environment-agnostic (dev `telos.local` and prod domain both match):
```yaml
tcp:
  routers:
    turn-tls:
      entryPoints:
        - turn
      rule: "HostSNI(`*`)"
      tls: {}
      service: turn-livekit
  services:
    turn-livekit:
      loadBalancer:
        servers:
          - address: "livekit:5349"
```

- [ ] **Step 3: Publish 5349 on the traefik service in `docker-compose.yml`**

Find the traefik `ports:` block:
```yaml
    ports:
      - "80:80"
      - "443:443"
```
Change to:
```yaml
    ports:
      - "80:80"
      - "443:443"
      - "5349:5349" # TURN/TLS — terminated by Traefik, forwarded to livekit:5349
```

- [ ] **Step 4: Validate compose + restart traefik**

Run: `podman-compose config >/dev/null && echo OK`
Expected: `OK`.

> NOTE FOR EXECUTOR: restarting `telos-traefik` is production-affecting (it fronts the whole app). In auto mode this may be denied — if so, STOP and ask the user to run `! podman-compose up -d traefik` or grant permission.

Run: `podman-compose up -d traefik`
Then: `podman logs --tail 40 telos-traefik 2>&1 | grep -iE "turn|5349|error"`
Expected: entrypoint `turn` bound on `:5349`; TCP router `turn-tls` registered; no error about the router/service/cert.

- [ ] **Step 5: Verify TLS terminates on 5349 and forwards to LiveKit**

Run: `openssl s_client -connect telos.local:5349 -servername telos.local </dev/null 2>/dev/null | openssl x509 -noout -subject 2>/dev/null`
Expected: `subject=CN=telos.local` (dev) — proves Traefik terminates TLS on 5349 with the default cert. (A returned cert proves the entrypoint + router + TLS store are wired; it does not by itself prove TURN allocation, which Task 3 covers.)

- [ ] **Step 6: Commit**

```bash
git add config/traefik.yaml config/dynamic/turn.yaml docker-compose.yml
git commit -m "feat(voice): terminate TURN/TLS at Traefik on :5349 -> livekit"
```

---

### Task 3: Verify relay end-to-end (dev, via TURN/UDP) + regression

**Files:**
- Test: `frontend/e2e/voice.spec.ts` (existing — regression only, no edits expected)

**Interfaces:**
- Consumes: Tasks 1–2 (TURN listening + Traefik TLS). Uses the credentialed e2e account pattern from `frontend/e2e/files.spec.ts` (env `E2E_USERNAME`/`E2E_PASSWORD`; see the `telos-e2e-test-accounts` memory for minting one).

- [ ] **Step 1: Confirm the access token now advertises TURN/ICE servers**

Mint or reuse a Member account (see the e2e-test-accounts memory: insert invite, accept with `Origin: http://localhost:8080` header). Then request a voice token and decode it:
```bash
TOKEN=$(curl -s -X POST http://localhost:8080/api/v1/voice/channels/00000000-0000-0000-0000-000000000005/token \
  -H "Cookie: <session-cookie-from-login>" | sed -E 's/.*"token":"([^"]+)".*/\1/')
echo "$TOKEN" | cut -d. -f2 | base64 -d 2>/dev/null | tr ',' '\n' | grep -i turn || echo "no turn claim in token (LiveKit injects ICE servers at connect, not necessarily in the JWT)"
```
Expected: either a TURN-related claim, OR (acceptable) nothing — LiveKit v1.10 injects ICE server config in the signaling `JoinResponse` at connect time rather than the JWT. The authoritative check is Step 2 (a relay candidate actually appears).

- [ ] **Step 2: Join voice and confirm a `relay` ICE candidate appears in LiveKit logs**

With `podman logs -f telos-livekit` running in one shell, join `gaming-lounge` from a browser (or the probe below). In the `removing participant` / `ICE candidate pair stats` log lines, look for a candidate of `type(relay/)` — its presence proves the client used the TURN relay.

Reusable probe (fake media, drives the real prod build via Traefik). Create `frontend/e2e/_turn_probe_tmp.spec.ts`:
```ts
import { test, expect, type Page } from "@playwright/test";
const USERNAME = process.env.E2E_USERNAME, PASSWORD = process.env.E2E_PASSWORD;
test.skip(!USERNAME || !PASSWORD, "creds");
test.use({
  baseURL: "https://telos.local",
  ignoreHTTPSErrors: true,
  launchOptions: { args: ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream"] },
});
async function login(page: Page) {
  await page.goto("/login/");
  await page.locator("#username").fill(USERNAME!);
  await page.locator("#password").fill(PASSWORD!);
  await page.getByRole("button", { name: "Enter the node" }).click();
  await page.waitForURL("**/chat/**");
}
test("turn probe", async ({ page }) => {
  await login(page);
  const ch = page.locator(".chan", { has: page.locator(".joinlbl") }).first();
  await ch.waitFor({ state: "visible", timeout: 10_000 });
  await ch.click();
  const dock = page.getByTestId("voice-dock");
  await expect(dock).toBeVisible({ timeout: 15_000 });
  for (let i = 0; i < 12; i++) {
    await page.waitForTimeout(2000);
    const err = await page.getByTestId("voice-dock-error").isVisible();
    const txt = (await dock.innerText()).replace(/\n+/g, " | ");
    console.log(`TURN t=${(i + 1) * 2}s error=${err} dock="${txt}"`);
    if (err || txt.includes("connected")) break;
  }
});
```
Run (from `frontend/`, dev server + stack up):
`E2E_USERNAME=<u> E2E_PASSWORD=<p> npx playwright test _turn_probe_tmp --reporter=line`
Expected in dev: because the self-signed cert blocks `turns://`, the TLS relay will NOT validate — but the TURN/UDP path (`3478`, no cert) can still relay. In the LiveKit logs, a `type(relay/)` candidate pair should reach `state: succeeded` (contrast the earlier all-`failed` UDP-host pairs). If the browser still can't relay in the local hairpin environment, capture the LiveKit `ICE candidate pair stats` showing the relay candidate was at least *offered* (proves TURN is serving allocations), and note that full relay success requires prod (valid cert) or an off-host client.

- [ ] **Step 3: Delete the probe**

```bash
rm -f frontend/e2e/_turn_probe_tmp.spec.ts
```

- [ ] **Step 4: Regression — existing voice e2e still passes**

Run (from `frontend/`): `E2E_USERNAME=<u> E2E_PASSWORD=<p> npx playwright test voice --reporter=line`
Expected: `2 passed` (run in isolation — see the e2e-test-accounts memory re: parallelism).

- [ ] **Step 5: Clean up the test account**

Per the e2e-test-accounts memory: delete the invite row scoped to `used_by = <uid>`, then the user.

- [ ] **Step 6: Commit (if any tracked files changed)**

No tracked-file changes expected in this task (probe is deleted). If verification revealed a needed config tweak, commit it with an explanatory message. Otherwise skip.

---

### Task 4: Document firewall/port requirements

**Files:**
- Modify: `docs/superpowers/specs/2026-07-13-livekit-turn-server-design.md` (append an "Operator runbook" note) OR create `docs/voice-turn-ports.md`

**Interfaces:**
- Produces: operator-facing documentation of the ports to forward. No runtime effect.

- [ ] **Step 1: Write the port-forwarding runbook**

Create `docs/voice-turn-ports.md`:
```markdown
# Voice (LiveKit TURN) — firewall / port-forwarding

TURN only helps if these reach the host from the internet. Forward to the
Telos host:

| Port        | Proto | Purpose                                  |
|-------------|-------|------------------------------------------|
| 5349        | TCP   | TURN/TLS (terminated by Traefik)         |
| 3478        | UDP   | TURN/UDP (plain relay)                   |
| 50000–50100 | UDP   | Direct WebRTC media (non-relay ICE)      |
| 443         | TCP   | HTTPS + signaling (already required)     |

Notes:
- TURN/TLS (5349) validates only against a cert the client trusts — works in
  production (Let's Encrypt), not with the dev self-signed telos.local cert.
- Same-LAN clients still hairpin (they resolve the domain to the public IP the
  router won't loop back); test from off-network, or add split-horizon DNS.
- To forward fewer UDP ports, collapse the media range to a single
  `rtc.udp_port` in config/livekit.yaml (separate optional change).
```

- [ ] **Step 2: Commit**

```bash
git add docs/voice-turn-ports.md
git commit -m "docs(voice): TURN firewall/port-forwarding runbook"
```

---

## Final verification (after all tasks)

1. `podman-compose config >/dev/null` — parses.
2. `podman logs telos-livekit | grep -i turn` — TURN enabled, listening 5349/3478, domain = `TELOS_DOMAIN`.
3. `podman logs telos-traefik | grep -iE "turn|5349"` — entrypoint + TCP router registered, no errors.
4. `openssl s_client -connect telos.local:5349 -servername telos.local` — Traefik serves the cert on 5349.
5. LiveKit `ICE candidate pair stats` show a `type(relay/)` candidate when joining voice (relay is being offered/used).
6. `npx playwright test voice` — regression green.
7. `curl http://localhost:8080/api/v1/health` — healthy.
