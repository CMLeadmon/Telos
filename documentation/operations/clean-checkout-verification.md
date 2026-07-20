# Clean-Checkout Verification

Phase 1 (P1-T5) evidence summary. Stable, sanitized fields copied from the
preview verification evidence produced by
`scripts/verify-clean-checkout.sh --source-ref <candidate> --evidence-out <path>`
run against the staged Phase 1 implementation tree. Smoke-environment
credentials are generated randomly per run, held in a mode-0600 file inside
the disposable export, and never recorded here. This document intentionally
records no candidate commit/tree ID; the accepted commit carries its own
`Verified-Tree` trailer.

## Verification command

```bash
bash scripts/verify-clean-checkout.sh --source-ref "$CANDIDATE_COMMIT" \
  --evidence-out /tmp/telos-phase1-final.json
```

The script exports the exact tree-ish to a temporary directory (never copying
`.git`, `.env`, certificates, caches, or generated output), generates a
random smoke environment, validates the Compose model, builds the image from
the export, boots an isolated `telos-core` + `postgres` + `redis` project on
a random loopback port with project-scoped container names, networks, and
volumes, probes the gateway, writes evidence outside the repository, and
tears everything down.

## Stable evidence fields

| Field | Value |
|---|---|
| Tool | podman version 5.8.4 |
| Compose services (validated model) | traefik, telos-core, postgres, redis, clamav, jellyfin, grimmory-db, grimmory, livekit |
| Builder images | node:24.18.0-alpine, golang:1.26.5-alpine, alpine:3.22.2 |
| Inventory check | pass (migrations 0001–0007 contiguous; all release inputs tracked; pinned toolchains asserted) |
| Compose validation | pass |
| Image build from export | pass |
| Gateway liveness (`GET /api/v1/health`) | HTTP 200 |
| Static assets (`GET /`) | HTTP 200 |
| First-owner bootstrap (`POST /api/v1/auth/bootstrap`) | HTTP 201 |
| Login issuing `telos_session` cookie (`POST /api/v1/auth/login`) | HTTP 200 |
| Teardown | pass (containers, networks, volumes, image, and export removed) |

## Exit-code contract

Proven by `scripts/tests/verify-clean-checkout-test.sh`: 10 for a missing
required input or migration gap, 20 for a forced build failure, 30 for a
failed readiness probe, 40 for a corrupted evidence artifact.
