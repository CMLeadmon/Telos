# Candidate Supply Chain

> **Status:** the scan, its policy and its tests are implemented, and the scan has run live against a rehearsal candidate
> (`docs/implementation/standalone-beta-progress.md`). That run **failed on real findings in the pinned images**; the gate
> stays red until those are fixed or accepted by a person. A rehearsal run is never certification evidence.

The `candidate-supply-chain` gate judges one frozen candidate: every image and desktop artifact in its graph, and the exact
source tree it was built from. It runs only after the freeze, from the SBOMs the builder wrote.

```bash
scripts/scan-candidate.sh --candidate-lock LOCK --artifact-root DIR --sbom-dir DIR --result-out OUT/result.json --source-dir TREE
scripts/scan-candidate.sh --rehearsal-lock LOCK [--source-commit SHA] ...      # a rehearsal graph; the record says so
```

`TREE` is the extracted source of the candidate (`git archive` output, **including its top directory**: the scanner reads the
tree's own `.gitleaks.toml` from there). Tools (`syft`, `trivy`, `gitleaks`) are downloaded once by digest from `ci/tools.lock`.

## What is judged

| Check | Fails when |
|---|---|
| SBOMs | one is missing, stale, not bound to its artifact, or not made by the locked Syft |
| Secrets (gitleaks, the tree's reviewed `.gitleaks.toml` — its digest is in the report) | any finding. The allowlist names exact values, never paths |
| Vulnerabilities (Trivy, database at most 24 h old, digest recorded) | any Critical; any High without an exact, unexpired entry in `ci/risk-acceptances.json` |
| Licenses (`ci/license-policy.json`) | linked code (Go modules, npm packages, Cargo crates) with no license, a forbidden or an unlisted one; copyleft service code linked into Telos code |

A scan that cannot run (no database, a tool failure) is a refusal, never a pass.

## The license policy (schemaVersion 3)

- `linkedArtifacts` — per ecosystem, the licenses linked code may carry.
- `isolatedServices` — third-party images that run as their own containers. They are inventoried, not judged.
  `linkingForbidden: true` marks copyleft services (Jellyfin, Grimmory, MariaDB, ClamAV, Squid) whose code must never appear in
  linked code; permissive services (Redis, Prometheus, Traefik …) are judged by their licenses like any dependency, so Telos
  may link the Redis and Prometheus *client libraries*.
- `reviewedLicenses` — a Go binary carries no license files, so its SBOM cannot know them. Each linked module has an entry
  keyed by exact `name@version` with the license and its evidence (the LICENSE file of that version). A dependency change is a
  finding until someone reads the new license; `tests/operations/supply_chain_test.py` fails when `backend/go.mod` and this list
  disagree. An SBOM-declared license is never overridden.

## Reading a failing scan

A failing record keeps the first 200 findings in `scan-report.json`; the command prints the first 50. Each line names the
artifact, advisory, package and version. A High can be accepted only by an entry that names exactly that advisory, package,
version and artifact, with an owner, mitigation and an expiry at most 30 days away. A Critical cannot be accepted: fix it by
moving the pin, or replace the image.

The pins live in `release/images.lock`; `scripts/refresh-image-lock.py` re-resolves one image at a time into a reviewable diff.
Moving a pin changes what runs, so re-run the stack's rehearsals after each.
