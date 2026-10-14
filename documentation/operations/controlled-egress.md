# Controlled Egress

Telos bundles isolated third-party services (ClamAV, Jellyfin, Grimmory) that
each want to reach the Internet — for signature updates and metadata/artwork
enrichment. Left unconstrained, any of them (or a compromise of one) could
exfiltrate data or be steered at an internal address. Telos removes their direct
Internet path and routes every outbound request through a single deny-by-default
proxy.

## Topology

- `ClamAV`, `Jellyfin`, `Grimmory`, `postgres`, `redis`, and `grimmory-db` sit
  on **internal** networks with no route off-node.
- `telos-egress-proxy` (Squid, `deploy/egress-proxy/Dockerfile`) is **dual-homed**:
  it is reachable by those services on `telos-backend` and is the only service
  besides Traefik that joins the non-internal `telos-egress` network.
- ClamAV/Jellyfin/Grimmory are pointed at the proxy with `http_proxy` /
  `https_proxy` (and JVM `-Dhttp(s).proxyHost` for Grimmory). Their only Internet
  path is the proxy.
- `telos-core`'s own candidate-cover downloads do **not** use Squid; they are
  constrained in-process by the same hostname allowlist plus public-address-only
  DNS/redirect SSRF validation (`backend/egress.go`, `backend/library.go`).

## Policy (`config/egress/squid.conf`)

Deny-by-default. A request is allowed only if **all** hold:

- Method/port: `CONNECT` only to 443; plaintext only to provider-required 80;
  every other port denied.
- Destination host is on the reviewed allowlist
  (`config/egress/allowed-domains.txt`, a Squid `dstdomain` list — an IP literal
  never matches a hostname entry, so IP-literal `CONNECT` is refused).
- The **resolved** address is public: `10/8`, `172.16/12`, `192.168/16`,
  `127/8`, `169.254/16` (link-local, incl. cloud metadata), `100.64/10` (CGNAT),
  multicast, and reserved ranges are denied *after* DNS resolution, so a
  rebinding or mixed answer cannot slip an internal target through.
- The client is on the backend network; the cache-manager/admin interface is
  denied outright.

The proxy hides its identity (`via off`, `forwarded_for delete`,
`httpd_suppress_version_string on`) and redacts logged URLs
(`strip_query_terms on`) so query-string tokens never land in the access log.

## Reviewed destinations (initial set)

`database.clamav.net`, `www.googleapis.com`, `books.google.com`,
`books.googleusercontent.com`, `openlibrary.org`, `covers.openlibrary.org`,
`api.themoviedb.org`, `image.tmdb.org`, `www.omdbapi.com`.

## Adding a domain (operator process)

1. Confirm the host is required by an enabled integration and review its privacy
   implications.
2. Append the exact host to `config/egress/allowed-domains.txt` (a leading dot
   also matches subdomains and the apex). If the host serves covers fetched by
   `telos-core`, also add it to `coverEgressAllowedHosts` in `backend/egress.go`.
3. Run `sh scripts/tests/egress_test.sh` (add `EGRESS_LIVE=1` for the live
   allow/deny probes) and `podman-compose --env-file tests/fixtures/compose.env
   config`.
4. Record the addition and its justification in this document's change history.

## Privacy, retention, and failure behavior

- **Expected external traffic** is exactly the reviewed set above — signature
  updates and metadata/artwork lookups. No analytics, telemetry, or AI endpoints.
- **Access logs** are query-redacted and should be retained only as long as the
  operator needs for troubleshooting.
- **Fail-closed**: when an approved provider is unreachable, enrichment degrades
  (a missing cover/metadata field) rather than falling back to an uncontrolled
  path. No upstream administrative interface is ever published.

## Verification

- `bash scripts/drills/egress.sh --target-file TARGET.json (--rehearsal --source-commit SHA | --candidate-lock LOCK)
  --result-out OUT/result.json` — **the live drill** (the `controlled-egress` gate), run from a machine that is not the
  node. A locked curl image (`toolImages.egress-probe` in `release/images.lock`) joins each service's *network
  namespace* (by path: Compose puts the containers in a pod, which rules out `--network container:`) with no
  capabilities, a read-only root and no volumes, and records only status and exit codes:
  - ClamAV, Jellyfin, Grimmory, PostgreSQL, Redis, MariaDB and Prometheus cannot reach the Internet directly, by
    hostname (pinned with `--resolve`, since a joined namespace has no resolver of its own) or by IP literal;
  - the three proxied services reach an allowlisted HTTPS host through the proxy (if the node's own proxy namespace
    cannot reach it either, the allowed path is `not_run`, not a pass);
  - through the proxy an unlisted domain, an IP literal, a private address, the link-local metadata address, an
    alternate port and plaintext to an unlisted host are all refused;
  - a request carrying a random query token is logged without the token (the program reports a verdict, never log text);
  - the containers' and networks' identities are bound to the run.
  A probe that could not run (podman failed) is `not_run`: only a *network* failure counts as isolation.
  `telos-core` and Traefik keep their own egress by design (the gateway's cover downloads are allowlisted in-process;
  Traefik needs ACME), so they are outside the "direct path must fail" set.

- `sh scripts/tests/egress_test.sh` — static policy assertions plus a real
  `squid -k parse`. With `EGRESS_LIVE=1` it boots the proxy on an isolated
  network and asserts an allowlisted host succeeds (`200`) while an unlisted host
  and an alternate port are refused.
- `podman-compose --env-file tests/fixtures/compose.env config` — confirms the
  topology renders and no upstream admin port is published.
