# Voice (LiveKit TURN) — operator runbook

The embedded LiveKit TURN server gives WebRTC media a relay fallback so remote
and UDP-restricted clients can connect. It only works if DNS, the cert, and the
firewall are set up as below. Signaling (the WebSocket join) is unrelated and
already rides `wss://<domain>/livekit` on 443.

## What LiveKit advertises to clients

Verified from the browser's `RTCPeerConnection.setConfiguration`:

```
turn:<PUBLIC_IP>:3478?transport=udp        # UDP relay — the workhorse
turns:turn.<domain>:443?transport=tcp      # TLS relay — fallback for UDP-blocked networks
```

- The UDP URL uses the node's public IP (from `use_external_ip: true`).
- The TLS URL uses the `turn.<domain>` subdomain on 443. LiveKit hardcodes port
  443 for `external_tls`; it is not configurable. Traefik demuxes that SNI on
  the shared 443 entrypoint (TCP router, docker labels on the `livekit` service)
  and forwards plain TCP to `livekit:5349`.

## DNS (required for the TLS fallback)

- Add a DNS record **`turn.<domain>`** resolving to the same host as `<domain>`.
- Local dev: add `turn.telos.local` to `/etc/hosts` (→ 127.0.0.1) if you want to
  exercise the TLS path locally. (It still won't validate — see Cert.)

## Cert (required for the TLS fallback)

- The TLS cert Traefik serves must cover **`turn.<domain>`** — add it as a SAN
  on the Let's Encrypt cert, or use a wildcard `*.<domain>`. Without it, browsers
  reject `turns://turn.<domain>` and the TLS relay is unused (UDP still works).
- Dev's self-signed `telos.local` cert does NOT cover `turn.telos.local`, so the
  TLS relay cannot be validated in dev. Dev verification relies on the UDP relay
  and the direct path.

## Firewall / port-forwarding (host)

Forward to the Telos host:

| Port        | Proto | Purpose                                             |
|-------------|-------|-----------------------------------------------------|
| 443         | TCP   | HTTPS + signaling + **TURN/TLS** (already required) |
| 3478        | UDP   | **TURN/UDP relay** — forward this to fix most remote clients |
| 50000–50100 | UDP   | Direct (non-relay) WebRTC media                     |

Notes:
- **Forwarding UDP 3478 is the single change that fixes the reported failure**
  for most remote clients — it enables the UDP relay LiveKit already advertises.
- 443 is already forwarded (the web app works), so the TLS fallback needs no new
  port — only the `turn.<domain>` DNS record and cert SAN above.
- The TURN relay-allocation range (`30000–40000`, from the startup log) is
  SFU-local (TURN relays to the co-located media server) and does **not** need
  host exposure.

## What cannot be verified on the host itself

Same-host / same-LAN clients hairpin: they resolve `<domain>`/`turn.<domain>` to
the public IP, which the router won't loop back. So the relay can only be proven
from an **off-network client** (cellular, another site). Locally the call still
ends in `could not establish pc connection` even with TURN correct — that is the
hairpin limit, not a TURN misconfiguration. For same-LAN use, add split-horizon
DNS (resolve the domain to the LAN IP internally); that is a separate change.

## Reducing forwarded UDP ports (optional)

To avoid forwarding the 101-port `50000–50100` range, collapse direct media to a
single mux port: set `rtc.udp_port: 7882` (and remove `port_range_start/end`) in
`config/livekit.yaml`, then forward only UDP 7882. The TURN relay (3478) is
unaffected.
