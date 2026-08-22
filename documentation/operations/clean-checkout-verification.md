# Clean-Checkout Verification

This runbook describes a candidate-specific verification procedure; it is not
proof that any prior candidate remains shipping-ready. Each invocation writes
its own sanitized evidence artifact for the exact source reference. Smoke-environment
credentials are generated randomly per run, held in a mode-0600 file inside
the disposable export, and never recorded here.

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
volumes, probes the gateway, writes the evidence artifact outside the repository, and
tears everything down.

## Current model inventory

| Field | Value |
|---|---|
| Compose services | traefik, telos-migrate, telos-audiobook-migrate, telos-core, postgres, redis, telos-egress-proxy, clamav, jellyfin, grimmory-db, grimmory |
| Migration inventory | 0001–0023; the verifier requires a contiguous tracked sequence |
| Runtime tool and builder images | Recorded by the candidate invocation |
| Compose, image, health, static asset, bootstrap, login, and teardown results | Candidate-specific; read the emitted evidence artifact |

## Exit-code contract

Proven by `scripts/tests/verify-clean-checkout-test.sh`: 10 for a missing
required input or migration gap, 20 for a forced build failure, 30 for a
failed readiness probe, 40 for a corrupted evidence artifact.
