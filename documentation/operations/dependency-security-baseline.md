# Dependency Security Baseline

P2-T1 record. Scanners: `govulncheck` (latest release; the plan's pinned
v1.1.2 does not compile under Go 1.26 due to an old `x/tools`
incompatibility, so the current release is the reviewed substitution) run in
the `golang:1.26.5` container, and `npm audit --omit=dev` under the Node
24.18.0 lockfile. Only package/advisory/version data is recorded.

## Failing baseline (before remediation)

Go — 4 reachable vulnerabilities:

| Advisory | Module | Found | Fixed in |
|---|---|---|---|
| GO-2026-5004 | github.com/jackc/pgx/v5 | v5.6.0 | v5.9.2 |
| GO-2026-4945 | github.com/go-jose/go-jose/v3 | v3.0.0 | v3.0.5 |
| GO-2025-3540 | github.com/redis/go-redis/v9 | v9.5.3 | v9.6.3 |
| GO-2024-2687 | golang.org/x/net | v0.17.0 | v0.23.0 |

npm production — 2 findings (1 critical, 1 moderate): `xmldom *` (multiple
advisories, package abandoned) via `epubjs 0.4.2 → xmldom ^0.1.27`.

## Remediation and resolved versions

Go module floors met or exceeded (each graph change reviewed — direct floors
plus their minimal transitive requirements only):

| Module | Resolved |
|---|---|
| github.com/jackc/pgx/v5 | v5.9.2 |
| github.com/redis/go-redis/v9 | v9.6.3 |
| github.com/go-jose/go-jose/v3 | v3.0.5 |
| golang.org/x/net | v0.53.0 |
| golang.org/x/crypto | v0.50.0 (pulled by x/net v0.53.0) |
| github.com/jackc/puddle/v2, pgservicefile | pgx v5.9.2 requirements |

npm: `epubjs` stays at the pinned 0.4.2; the abandoned `xmldom` is overridden
to the maintained API-compatible fork `npm:@xmldom/xmldom@0.9.10` (epubjs
touches it only in non-browser fallback paths; Telos uses the browser build
with native `XMLSerializer`). Exactly one production resolution of `xmldom`
remains in the lockfile. `next` 16.2.10 and the `postcss` 8.5.10 override are
unchanged from Phase 1.

## Post-remediation scanner summaries

- `govulncheck ./...`: **“Your code is affected by 0 vulnerabilities.”**
  (Informational: unreachable findings remain in imported-but-uncalled code.)
- `npm audit --omit=dev --audit-level=moderate`: **0 vulnerabilities.**
- `go test ./...`, `npm run lint`, `npm run test:unit`, `npm run build`: pass.
