# Voice TURN — Setup Guide (operator steps)

The code/config is done and committed. Voice media won't work in production until
you complete the network + DNS + cert steps below. Do them in order. **Phase 1
alone fixes voice for most remote users**; Phase 2 is hardening for users on
locked-down networks.

Placeholders to substitute:
- `<domain>`   → your real public domain (e.g. `telos.example.com`)
- `<public-ip>`→ your home connection's public IP (LiveKit auto-detects it; today it's 45.52.222.119, but residential IPs can change)
- `<host-lan-ip>` → the Telos host's LAN address (currently 192.168.86.22)

---

## Phase 0 — Deploy the config to your prod host

The TURN changes live in these files (already committed on `docs-rewrite`):
`config/livekit.yaml`, `docker-compose.yml`.

1. Get those changes onto the prod host (merge `docs-rewrite`, or copy the two
   files). If your prod host is the same machine you've been testing on, the
   config is **already live** — skip to step 3.
2. Confirm `.env` on prod has your **real** domain, not telos.local:
   ```
   TELOS_DOMAIN=<domain>
   ```
   (LiveKit's TURN domain is derived as `turn.<domain>` automatically.)
3. Recreate the two services:
   ```bash
   podman-compose up -d livekit traefik
   ```
4. Confirm TURN started and picked up the right domain:
   ```bash
   podman logs telos-livekit  | grep -i "Starting TURN"
   podman exec telos-livekit sh -c 'echo $LIVEKIT_TURN_DOMAIN'   # -> turn.<domain>
   ```

---

## Phase 1 — UDP relay (fixes most remote users)

This is the high-impact step. LiveKit already advertises a UDP relay at
`turn:<public-ip>:3478`; it just needs to be reachable from the internet.

### Step 1 — Forward UDP 3478 on your router

In your router's port-forwarding / NAT section, add:

| Name       | External port | Protocol | Internal IP     | Internal port |
|------------|---------------|----------|-----------------|---------------|
| telos-turn | 3478          | UDP      | `<host-lan-ip>` | 3478          |

(The host already publishes `3478/udp`; you're just opening the router to it.)

### Step 2 — (Optional) forward the direct-media range

Lets clients that *can* do direct UDP skip the relay (lower latency, less load):

| Name         | External port | Protocol | Internal IP     | Internal port |
|--------------|---------------|----------|-----------------|---------------|
| telos-media  | 50000-50100   | UDP      | `<host-lan-ip>` | 50000-50100   |

If your router dislikes a 101-port range, skip this — the relay covers everyone.
(Or collapse it to one port: set `rtc.udp_port: 7882` in `config/livekit.yaml`,
remove `port_range_start/end`, restart livekit, and forward only UDP 7882.)

### Step 3 — Test from OFF your network

This is the only valid test — same-WiFi devices hairpin and will still fail.

1. Take a phone **off WiFi** (cellular), or use a device on a different network.
2. Open `https://<domain>`, log in, join a voice channel.
3. It should reach **"voice connected"** in the dock (not the error state).

Watch it work from the server side:
```bash
podman logs -f telos-livekit | grep -iE "ICE candidate pair|relay|state"
```
Success looks like an ICE pair with `type(relay/...)` reaching `state: succeeded`
(contrast the earlier all-`failed` pairs). If it still fails, see Troubleshooting.

**If voice works off-network here, you're done for most users.** Phase 2 only
matters if some users on strict corporate/hotel networks still can't connect.

---

## Phase 2 — TLS relay fallback (for UDP-blocked networks)

Some networks block UDP entirely. For those users, LiveKit also advertises
`turns:turn.<domain>:443` (TLS over 443 — looks like normal HTTPS, gets through
almost anything). Traefik already routes this SNI to LiveKit; it needs DNS + a
cert covering the subdomain. **443 is already open**, so no new port.

### Step 4 — Add a DNS record for the TURN subdomain

Create an A (and AAAA if you use IPv6) record:
```
turn.<domain>   A   <public-ip>
```
Point it at the same place `<domain>` points. If `<domain>` is behind dynamic DNS,
add `turn.<domain>` to the same dynamic-DNS updater.

### Step 5 — Make your TLS cert cover `turn.<domain>`

Your cert must be valid for `turn.<domain>` or browsers reject the `turns://`
connection. Pick whichever matches how you issue certs:

- **Wildcard (cleanest):** issue `*.<domain>` via a DNS-01 challenge. It covers
  `turn.<domain>` with no per-host work, now and forever.
- **Add a SAN:** reissue your cert for both names, e.g. with certbot:
  ```bash
  certbot certonly -d <domain> -d turn.<domain>
  ```
  then point Traefik at the updated cert files (same place your current cert
  loads from).
- **Traefik ACME resolver:** if Traefik obtains certs itself, ensure a router
  references `turn.<domain>` so it requests that cert (a wildcard via DNS-01 is
  still simplest).

Reload Traefik after the cert is in place:
```bash
podman-compose up -d traefik
```

### Step 6 — Verify the TLS path

From off-network, on a device/network that blocks UDP (or force TCP), join voice.
Or verify the endpoint presents the right cert:
```bash
openssl s_client -connect turn.<domain>:443 -servername turn.<domain> </dev/null \
  2>/dev/null | openssl x509 -noout -subject
# subject should show <domain> or turn.<domain> from your real CA (not telos.local)
```

---

## Troubleshooting

- **Still "could not establish pc connection" off-network after Phase 1** → UDP
  3478 isn't actually reaching the host. Re-check the router rule targets
  `<host-lan-ip>`, and that no ISP/CGNAT sits in front (if your "public IP" is in
  100.64.0.0/10 you're behind carrier-grade NAT and can't port-forward — you'd
  need a VPS relay or Tailscale instead).
- **Confirm what the client is told to use** (should list both relays):
  browser devtools → the LiveKit connection, or check `podman logs telos-livekit`
  for the participant's candidates.
- **Cert errors on voice only** → Phase 2 cert doesn't cover `turn.<domain>`.
- **Same-house testing keeps failing** → expected (hairpin NAT). Test off-network,
  or set up split-horizon DNS so LAN devices resolve `<domain>`/`turn.<domain>`
  to `<host-lan-ip>` directly.

## Rollback

TURN is additive; to disable, set `turn.enabled: false` in `config/livekit.yaml`
and `podman-compose up -d livekit`. Voice returns to direct-media-only (the prior
behavior).

## Reference

Port/cert reference table: `docs/voice-turn-ports.md`.
