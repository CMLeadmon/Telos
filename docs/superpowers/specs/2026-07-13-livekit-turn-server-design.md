# LiveKit TURN Server — Design

Date: 2026-07-13
Status: Approved

## Problem

Voice calls connect at the signaling layer but fail at media: the browser joins
the LiveKit room over TCP/443 (through Traefik) and then the WebRTC peer
connection never establishes — `could not establish pc connection`. LiveKit's
ICE candidate-pair stats show every pair `failed` with `requestsSent: N,
responsesReceived: 0`: the client cannot deliver UDP media to the server's
advertised endpoints, and there is **no TURN relay** to fall back on.

Confirmed root cause: WebRTC media (ICE) has no working path — direct UDP is
blocked/unreachable for the client, and no relay exists. This affects
genuinely-remote clients and clients on UDP-restricted networks. (A separate
same-LAN hairpin-NAT case exists for testing from the server's own network; it
is out of scope here and is handled by testing off-network or split-horizon DNS.)

## Deployment context (verified)

- Production: real public domain, Let's Encrypt cert, terminated by Traefik.
- Dev/local: self-signed cert `CN=telos.local` (SANs: telos.local, localhost,
  127.0.0.1), served by Traefik via the file provider (`config/dynamic/`).
- Traefik v3.3 fronts everything; entrypoints `web` (:80 → redirect) and
  `websecure` (:443). Domain is parameterized as `TELOS_DOMAIN` (`.env.example`).
- LiveKit v1.10, reached at `wss://<domain>/livekit` (Traefik strips `/livekit`
  → `livekit:7880`). `config/livekit.yaml`: `use_external_ip: true`,
  `tcp_port: 7881`, UDP media range `50000–50100`. No TURN configured.

## Design

Enable LiveKit's **embedded** TURN server (no new container) with TLS terminated
by Traefik. LiveKit auto-issues TURN credentials inside the access token the
gateway already mints, so **no frontend or gateway code changes** are required.

### Listeners

- **TURN/TLS on `:5349`** — Traefik terminates TLS (reusing the cert it already
  serves) and forwards plain TCP to LiveKit's TURN, which runs with
  `external_tls: true`. Firewall-traversing path; looks like HTTPS.
- **TURN/UDP on `:3478`** — plain TURN (already published in compose) for
  clients that are not UDP-blocked; lower latency than the TLS relay.
- Relayed media continues to use the existing UDP range `50000–50100`, which
  stays published.

### Config changes (four files)

1. **`config/livekit.yaml`** — add:
   ```yaml
   turn:
     enabled: true
     domain: ${TELOS_DOMAIN}   # resolved to a literal at deploy; dev=telos.local
     tls_port: 5349
     udp_port: 3478
     external_tls: true         # Traefik terminates TLS; LiveKit listens plain
   ```
   `domain` must not be hardcoded — it is templated from `TELOS_DOMAIN` so dev
   and prod differ without code edits. (LiveKit's static YAML does not do env
   interpolation itself; the implementation plan determines the exact templating
   mechanism — e.g. an entrypoint envsubst or a per-environment config file —
   and verifies it against the running container.)

2. **`config/traefik.yaml`** — add a `turn` entrypoint:
   ```yaml
   entryPoints:
     turn:
       address: ":5349"
   ```

3. **`config/dynamic/`** — a **TCP** router with TLS termination bound to the
   `turn` entrypoint, `HostSNI` on the TURN domain (browsers send SNI for
   `turns://<domain>`), routing to a TCP service targeting `livekit:5349`. Reuses
   the same cert store already configured for the `websecure` routers.

4. **`docker-compose.yml`** — publish `5349:5349` on the **traefik** service;
   confirm `3478/udp` and `50000-50100/udp` remain published on **livekit**.
   Ensure livekit is reachable from Traefik on 5349 over `telos-ingress`.

### Cert behavior (dev vs prod)

TURN/TLS trust follows the web app's cert:
- **Prod (Let's Encrypt):** `turns://<domain>:5349` validates in browsers — the
  relay works for real remote clients. This is the case that fixes the failure.
- **Dev (self-signed):** browsers will not validate `turns://` against the
  self-signed cert, so TURN/TLS does not relay in dev. Dev verification uses
  TURN/**UDP** on `:3478` (no cert validation) plus the direct path, which is
  sufficient to prove the relay end-to-end.

## Out of scope

- Same-LAN hairpin NAT (test off-network or add split-horizon DNS later).
- Collapsing the `50000–50100` range to a single `rtc.udp_port` (offered as an
  optional follow-up to reduce port-forwarding, not required here).
- Standalone coturn; native-LiveKit TLS termination (rejected: second
  cert-renewal path).

## Infrastructure requirements (operator responsibility)

Forward to the host (documented, not automated here):
- **TCP 5349** (TURN/TLS) and **UDP 3478** (TURN/UDP)
- **UDP 50000–50100** (relayed media)

## Testing / verification

No application code changes → no new unit tests; verification is config/log/
behavior based:
1. `podman-compose config` parses; Traefik and LiveKit start clean
   (`podman logs` shows TURN enabled and listening on 5349/3478, no bind errors).
2. **Relay proof (dev):** join voice with TURN/UDP; confirm LiveKit logs a
   `relay`-type ICE candidate pair reaching `state: succeeded` — the same
   `ICE candidate pair stats` log that previously showed `failed`.
3. **Token check:** the minted access token now carries TURN/ICE-server config
   so the client actually attempts the relay.
4. **Regression:** `frontend/e2e/voice.spec.ts` still passes; backend `go test`
   untouched and green.
