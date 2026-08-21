# Standalone Beta Phase 1: Truth and Gates Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate false-green release paths and establish machine-checkable product, gate, and evidence truth before beta implementation continues.

**Architecture:** Introduce one versioned evidence writer/verifier, one declarative gate catalog, and one argv-safe gate runner. Convert every success-printing placeholder into either an implemented check or an explicit `not_run` result. Make the four-module standalone beta boundary a JSON contract and verify it structurally against routes, frontend navigation, Compose services, dependencies, and removed-feature inventories.

**Tech Stack:** Bash, Python 3 standard library, JSON Schema draft 2020-12, Git, existing Go/TypeScript test suites, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-08-20-standalone-beta-readiness-design.md`

## Global Constraints

- Complete this plan before deployment, desktop, operations, or release scripts are allowed to emit beta success.
- A command that did not perform its declared check exits nonzero and records `not_run`; absence of an environment is not success.
- Shell wrappers reject unknown arguments, quote all paths, use `set -euo pipefail`, and pass commands as argv arrays. Never evaluate a catalog string with `eval`, `bash -c`, or `sh -c`.
- Evidence is candidate-bound, deterministic except for declared timestamps/runtime data, and contains no secrets or unredacted subprocess environment.
- Gate IDs are stable kebab-case identifiers. Renaming an ID requires updating the beta contract, catalog, evidence fixtures, and release verifier in the same commit.
- Keep historical specs and plans out of shipped-runtime scans, but do not exclude active code, current product documentation, manifests, Compose, CLI help, or operations scripts.
- Do not implement the later phase checks here. Their catalog entries must honestly emit `not_run` until the owning phase replaces the stub with real behavior.
- Run each focused test from the repository root.

---

## Current-State Baseline

The approved design records the complete audit in its **Current-State Findings** section. The phase-specific defects are directly visible in success-only certification (`scripts/certify-beta.sh:18-24`), runtime-hardening (`scripts/verify-runtime-hardening.sh:22-33`), capacity (`scripts/run-capacity-drill.sh:18-24`), and browser-matrix (`scripts/certify-browser-matrix.sh:18-24`) scripts. Product truth also still describes only three modules and a folded Files surface (`documentation/product/beta-feature-status.md:7-27`), while the development proxy retains a LiveKit target and route (`frontend/dev-server.mjs:13-35`). These are baselines to replace, not acceptable beta evidence.

---

## File Structure

- Create: `scripts/lib/evidence.py` — atomic evidence writer, canonical digest helper, and envelope verifier.
- Create: `tests/operations/evidence_envelope_test.sh` — positive and adversarial envelope tests.
- Modify: `release/evidence-envelope.schema.json` — strict version-2 schema.
- Modify: `scripts/verify-evidence.sh` — CLI wrapper for envelope and candidate binding verification.
- Create: `scripts/not-run.py` — uniform fail-closed result for checks awaiting an external environment or later implementation.
- Modify: `scripts/certify-beta.sh`, `scripts/verify-runtime-hardening.sh`, `scripts/run-capacity-drill.sh`, `scripts/audit-host-exposure.sh`, `scripts/certify-browser-matrix.sh`, `scripts/build-release.sh`, `scripts/build-predecessor.sh`, `scripts/stage-release.sh`, `scripts/sign-evidence.sh`, `scripts/verify-accumulated-suite.sh`, `scripts/verify-release-pins.sh`, `scripts/verify-restored-node.sh`, `scripts/verify-monitoring.sh`, `scripts/upgrade.sh`, `scripts/rollback.sh`, `scripts/provision-jellyfin.sh`, `scripts/provision-grimmory.sh`, `scripts/ci/run-local.sh`, `scripts/ci/validate-sbom.sh`, and `scripts/drills/*.sh` — remove false-success behavior.
- Create: `scripts/run-gates.py` — safe local/external gate executor.
- Modify: `ci/phase-gates.json` — declarative gate catalog.
- Modify: `tests/operations/accumulated_suite_test.sh`, `tests/operations/beta_certification_test.sh`, `tests/operations/ci_gates_test.sh` — behavioral rather than existence assertions.
- Create: `documentation/product/beta-contract.json` — machine-readable beta boundary.
- Modify: `documentation/product/beta-feature-status.md` — accurate four-module status and support boundary.
- Modify: `scripts/check-product-truth.sh` — structural contract validation plus current prohibited-surface scans.
- Create: `tests/operations/product_truth_test.sh` — mutation tests for structural claims.
- Modify: `frontend/dev-server.mjs` — remove LiveKit proxy target and branch.
- Modify: `README.md`, `CLAUDE.md`, `documentation/README.md`, `documentation/operations/backup-and-restore.md`, `documentation/operations/beta-certification.md`, `documentation/operations/browser-certification.md`, `documentation/operations/clean-checkout-verification.md`, `documentation/operations/container-hardening.md`, `documentation/operations/controlled-egress.md`, `documentation/operations/network-exposure.md`, `documentation/operations/release.md`, `documentation/operations/release-inputs.pathspec` — remove stale supported-product and completed-gate claims.
- Modify: `.github/workflows/ci.yml` — run honest Phase 1 gates.

---

### Task 1: Define and test evidence envelope v2

**Files:**
- Create: `scripts/lib/evidence.py`
- Create: `tests/operations/evidence_envelope_test.sh`
- Modify: `release/evidence-envelope.schema.json`
- Modify: `scripts/verify-evidence.sh`

**Interfaces:**
- Produces: `write`, `verify`, and `digest` subcommands in `scripts/lib/evidence.py`.
- Produces: envelope statuses `passed`, `failed`, `not_run` and canonical `sha256:` digests.
- Consumes later: all phase gate scripts and `scripts/run-gates.py`.

- [ ] **Step 1: Add a failing adversarial envelope test**

Create `tests/operations/evidence_envelope_test.sh` with a temporary candidate lock and assertions covering a valid `passed` envelope plus missing field, unknown status, mismatched candidate digest, non-array command, negative exit code, end-before-start, malformed digest, and secret-looking key rejection:

```bash
#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
fixture_dir="$(mktemp -d)"
trap 'rm -rf "$fixture_dir"' EXIT

printf '{"schemaVersion":1,"sourceCommit":"%040d"}\n' 0 >"$fixture_dir/candidate.json"
candidate_digest="sha256:$(sha256sum "$fixture_dir/candidate.json" | awk '{print $1}')"

python3 "$repo_root/scripts/lib/evidence.py" write \
  --output "$fixture_dir/passed.json" \
  --gate-id phase-1-self-test --status passed --reason "fixture assertions passed" \
  --source-commit "$(printf '%040d' 0)" --candidate-lock-digest "$candidate_digest" \
  --started-at 2026-08-20T10:00:00Z --finished-at 2026-08-20T10:00:01Z \
  --exit-code 0 --output-digest "sha256:$(printf passed | sha256sum | awk '{print $1}')" \
  --command-json '["printf","passed"]' \
  --tool-versions-json '{"python":"3"}' \
  --subject-json '{"kind":"fixture","digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}'

python3 "$repo_root/scripts/lib/evidence.py" verify \
  --schema "$repo_root/release/evidence-envelope.schema.json" \
  --candidate-lock "$fixture_dir/candidate.json" "$fixture_dir/passed.json"

for mutation in missing-gate unknown-status string-command negative-exit reversed-time bad-digest secret-field; do
  python3 "$repo_root/tests/helpers/mutate_evidence.py" \
    "$mutation" "$fixture_dir/passed.json" "$fixture_dir/$mutation.json"
  if python3 "$repo_root/scripts/lib/evidence.py" verify \
      --schema "$repo_root/release/evidence-envelope.schema.json" \
      --candidate-lock "$fixture_dir/candidate.json" "$fixture_dir/$mutation.json"; then
    echo "accepted invalid envelope: $mutation" >&2
    exit 1
  fi
done
```

Also create the narrowly scoped mutation helper `tests/helpers/mutate_evidence.py`; it loads one JSON document, applies only the named mutation, and writes sorted JSON. Do not use shell text replacement for JSON.

- [ ] **Step 2: Run the test and confirm the current placeholder verifier fails**

Run:

```bash
bash tests/operations/evidence_envelope_test.sh
```

Expected: nonzero because `scripts/lib/evidence.py` and the strict schema do not exist.

- [ ] **Step 3: Replace the evidence schema with a closed version-2 contract**

Set `release/evidence-envelope.schema.json` to draft 2020-12, `additionalProperties: false`, and require every field from the master plan's envelope. Encode these invariants in schema where possible:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": [
    "schemaVersion", "gateId", "status", "reason", "sourceCommit",
    "candidateLockDigest", "command", "startedAt", "finishedAt",
    "exitCode", "outputDigest", "toolVersions", "subject"
  ],
  "properties": {
    "schemaVersion": {"const": 2},
    "gateId": {"type": "string", "pattern": "^[a-z0-9]+(?:-[a-z0-9]+)*$"},
    "status": {"enum": ["passed", "failed", "not_run"]},
    "reason": {"type": "string", "minLength": 1},
    "sourceCommit": {"type": "string", "pattern": "^[0-9a-f]{40}$"},
    "candidateLockDigest": {"type": "string", "pattern": "^sha256:[0-9a-f]{64}$"},
    "command": {"type": "array", "minItems": 1, "items": {"type": "string"}},
    "startedAt": {"type": "string", "format": "date-time"},
    "finishedAt": {"type": "string", "format": "date-time"},
    "exitCode": {"type": "integer", "minimum": 0, "maximum": 255},
    "outputDigest": {"type": "string", "pattern": "^sha256:[0-9a-f]{64}$"},
    "toolVersions": {"type": "object", "additionalProperties": {"type": "string"}},
    "subject": {
      "type": "object",
      "additionalProperties": false,
      "required": ["kind", "digest"],
      "properties": {
        "kind": {"enum": ["source", "artifact", "runtime", "external-host", "fixture"]},
        "digest": {"type": "string", "pattern": "^sha256:[0-9a-f]{64}$"}
      }
    }
  }
}
```

- [ ] **Step 4: Implement the standard-library writer and verifier**

Implement `scripts/lib/evidence.py` with `argparse`, `json`, `hashlib`, `datetime`, `os`, and `tempfile`. The writer must serialize with `sort_keys=True` and `separators=(",", ":")`, `fsync` the temporary file, `chmod 0600`, and `os.replace` it. The verifier must perform the schema's type/pattern/enum checks without downloading a schema library, additionally require `finishedAt >= startedAt`, compute the candidate lock digest from raw bytes, compare it with `candidateLockDigest`, reject recursive keys matching `token|password|secret|credential|authorization` case-insensitively, and enforce the status/exit contract: `passed` has exit code `0`, `not_run` has exit code `3`, and `failed` has any nonzero exit code except `3`.

Expose the following stable CLI:

```text
evidence.py digest PATH
evidence.py write --output PATH --gate-id ID --status STATUS [WRITE_OPTION]
evidence.py verify --schema PATH --candidate-lock PATH ENVELOPE_1 [ENVELOPE_N]
```

`--schema` is required so a malformed or missing repository schema blocks verification even though validation code is local.

- [ ] **Step 5: Make `verify-evidence.sh` a strict wrapper**

Use this interface and reject every other argument:

```bash
bash scripts/verify-evidence.sh \
  --candidate-lock release/candidate-lock.json \
  --evidence-dir release/evidence
```

The wrapper resolves repository-relative paths, sorts `*.json` filenames bytewise, fails if no envelope exists, and invokes `evidence.py verify` once with the complete list.

- [ ] **Step 6: Run focused tests**

Run:

```bash
bash tests/operations/evidence_envelope_test.sh
bash -n scripts/verify-evidence.sh
python3 -m py_compile scripts/lib/evidence.py tests/helpers/mutate_evidence.py
```

Expected: all exit `0`; each negative mutation prints a specific validation failure to stderr.

- [ ] **Step 7: Commit the evidence contract**

```bash
git add release/evidence-envelope.schema.json scripts/lib/evidence.py scripts/verify-evidence.sh \
  tests/helpers/mutate_evidence.py tests/operations/evidence_envelope_test.sh
git commit -m "release: define fail-closed evidence envelopes"
```

---

### Task 2: Convert false-success scripts to explicit `not_run`

**Files:**
- Create: `scripts/not-run.py`
- Modify: `scripts/certify-beta.sh`
- Modify: `scripts/verify-runtime-hardening.sh`
- Modify: `scripts/run-capacity-drill.sh`
- Modify: `scripts/audit-host-exposure.sh`
- Modify: `scripts/certify-browser-matrix.sh`
- Modify: `scripts/build-release.sh`
- Modify: `scripts/build-predecessor.sh`
- Modify: `scripts/stage-release.sh`
- Modify: `scripts/sign-evidence.sh`
- Modify: `scripts/verify-release-pins.sh`
- Modify: `scripts/verify-restored-node.sh`
- Modify: `scripts/verify-monitoring.sh`
- Modify: `scripts/upgrade.sh`
- Modify: `scripts/rollback.sh`
- Modify: `scripts/provision-jellyfin.sh`
- Modify: `scripts/provision-grimmory.sh`
- Modify: `scripts/ci/run-local.sh`
- Modify: `scripts/ci/validate-sbom.sh`
- Modify: `scripts/drills/clean-node-install.sh`
- Modify: `scripts/drills/recovery.sh`
- Modify: `scripts/drills/upgrade-rollback.sh`
- Modify: `tests/operations/beta_certification_test.sh`

**Interfaces:**
- Produces: uniform exit `3` for `not_run` and an optional valid envelope.
- Consumes: evidence writer from Task 1.
- Preserves: later phase scripts may replace the wrapper while retaining their documented CLI.

- [ ] **Step 1: Replace the certification existence test with a false-green regression test**

In `tests/operations/beta_certification_test.sh`, invoke every listed script in its normal mode inside a loop. Assert that it exits `3`, does not contain `certified successfully`, `complete.`, or `passed.` in stdout, and writes a `not_run` envelope when given `--evidence-out`:

```bash
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
printf '{"schemaVersion":1,"sourceCommit":"%040d"}\n' 0 >"$tmp/candidate.json"

scripts=(
  certify-beta.sh verify-runtime-hardening.sh run-capacity-drill.sh
  audit-host-exposure.sh certify-browser-matrix.sh build-release.sh
  build-predecessor.sh stage-release.sh sign-evidence.sh verify-release-pins.sh
  verify-restored-node.sh verify-monitoring.sh upgrade.sh rollback.sh
  provision-jellyfin.sh provision-grimmory.sh ci/run-local.sh
  ci/validate-sbom.sh drills/clean-node-install.sh drills/recovery.sh
  drills/upgrade-rollback.sh
)

for script in "${scripts[@]}"; do
  set +e
  output="$(bash "scripts/$script" --candidate-lock "$tmp/candidate.json" \
    --evidence-out "$tmp/${script//\//-}.json" 2>&1)"
  status=$?
  set -e
  [[ $status -eq 3 ]] || { echo "$script returned $status" >&2; exit 1; }
  ! grep -Eqi 'certified successfully|complete\.|passed\.' <<<"$output"
  [[ "$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["status"])' "$tmp/${script//\//-}.json")" == not_run ]]
done
```

Add an assertion that an unknown flag exits `2` and does not write evidence.

- [ ] **Step 2: Run the test and observe the existing scripts claim success**

```bash
bash tests/operations/beta_certification_test.sh
```

Expected: nonzero and identifies the first false-green script.

- [ ] **Step 3: Implement `scripts/not-run.py`**

The helper accepts `--gate-id`, `--reason`, `--candidate-lock`, `--evidence-out`, and a `--`-terminated original argv array. When an evidence path is provided, require the candidate lock, compute its digest through `evidence.py`, write a `not_run` envelope with exit code `3`, and use an empty-output digest. Always print the specific form `not_run: candidate reference host unavailable` (with the caller's actual reason) to stderr and return `3`.

- [ ] **Step 4: Give every placeholder a strict CLI and corrective reason**

Each shell script must accept the common `--candidate-lock PATH` and `--evidence-out PATH` flags plus only its documented script-specific flags, reject every unknown or incomplete flag with exit `2`, and delegate to `not-run.py`. Use these gate IDs/reasons until their owner phase implements them:

| Script | Gate ID | Corrective reason |
|---|---|---|
| `certify-beta.sh` | `beta-certification` | `candidate certification is implemented in Phase 5` |
| `verify-runtime-hardening.sh` | `runtime-hardening` | `effective-runtime inspection is implemented in Phase 4` |
| `run-capacity-drill.sh` | `capacity` | `a candidate runtime and k6 environment are required` |
| `audit-host-exposure.sh` | `host-exposure` | `a candidate reference host is required` |
| `certify-browser-matrix.sh` | `browser-matrix` | `declared browser hosts and candidate URL are required` |
| `build-release.sh` | `release-build` | `immutable release building is implemented in Phase 5` |
| `build-predecessor.sh` | `predecessor-build` | `verified predecessor building is implemented in Phase 4` |
| `stage-release.sh` | `release-stage` | `atomic release staging is implemented in Phase 5` |
| `sign-evidence.sh` | `evidence-signing` | `operator signing is implemented in Phase 5` |
| `verify-release-pins.sh` | `release-pins` | `reviewed image and tool lock verification is implemented in Phase 4` |
| `verify-restored-node.sh` | `restored-node` | `separate-host restored-node verification is implemented in Phase 4` |
| `verify-monitoring.sh` | `monitoring` | `live monitoring verification is implemented in Phase 4` |
| `upgrade.sh` | `upgrade` | `generation-based upgrade is implemented in Phase 4` |
| `rollback.sh` | `rollback` | `generation-compatible rollback is implemented in Phase 4` |
| `provision-jellyfin.sh` | `jellyfin-guided-setup` | `use telos configure integrations after Phase 2` |
| `provision-grimmory.sh` | `grimmory-guided-setup` | `use telos configure integrations after Phase 2` |
| `ci/run-local.sh` | `local-ci` | `use the Phase 1 gate runner with a candidate lock` |
| `ci/validate-sbom.sh` | `sbom-validation` | `candidate SBOM validation is implemented in Phase 4` |
| `drills/clean-node-install.sh` | `clean-install` | `a clean Ubuntu reference host and Phase 2 drill implementation are required` |
| `drills/recovery.sh` | `separate-host-restore` | `a second Ubuntu reference host and Phase 4 recovery implementation are required` |
| `drills/upgrade-rollback.sh` | `upgrade-rollback` | `candidate and predecessor generations plus Phase 4 implementation are required` |

Retain `--validate-fixtures` only where a real fixture assertion exists. Delete modes that merely print validation success.

- [ ] **Step 5: Run behavioral and syntax tests**

```bash
bash tests/operations/beta_certification_test.sh
for script in scripts/{certify-beta,verify-runtime-hardening,run-capacity-drill,audit-host-exposure,certify-browser-matrix,build-release,build-predecessor,stage-release,sign-evidence,verify-release-pins,verify-restored-node,verify-monitoring,upgrade,rollback,provision-jellyfin,provision-grimmory}.sh scripts/ci/{run-local,validate-sbom}.sh scripts/drills/{clean-node-install,recovery,upgrade-rollback}.sh; do
  bash -n "$script"
done
python3 -m py_compile scripts/not-run.py
```

Expected: all tests exit `0`; direct normal execution still exits `3` by design.

- [ ] **Step 6: Commit the fail-closed wrappers**

```bash
git add scripts/not-run.py scripts/certify-beta.sh scripts/verify-runtime-hardening.sh \
  scripts/run-capacity-drill.sh scripts/audit-host-exposure.sh \
  scripts/certify-browser-matrix.sh scripts/build-release.sh scripts/build-predecessor.sh \
  scripts/stage-release.sh scripts/sign-evidence.sh scripts/verify-restored-node.sh \
  scripts/verify-release-pins.sh scripts/verify-monitoring.sh scripts/upgrade.sh scripts/rollback.sh \
  scripts/provision-jellyfin.sh scripts/provision-grimmory.sh scripts/ci \
  scripts/drills \
  tests/operations/beta_certification_test.sh
git commit -m "release: make unimplemented gates fail closed"
```

---

### Task 3: Implement the declarative gate catalog and safe runner

**Files:**
- Create: `scripts/run-gates.py`
- Modify: `ci/phase-gates.json`
- Modify: `scripts/verify-accumulated-suite.sh`
- Modify: `tests/operations/accumulated_suite_test.sh`
- Modify: `tests/operations/ci_gates_test.sh`

**Interfaces:**
- Produces: `python3 scripts/run-gates.py --catalog PATH --scope local|external --candidate-lock PATH --evidence-dir PATH [--gate ID]`, where `--gate` is repeatable.
- Produces: one output log and one evidence envelope per selected gate.
- Consumes: version-2 evidence writer and verifier from Task 1.

- [ ] **Step 1: Write catalog-validation and argv-safety tests**

Replace the current existence checks with temporary catalogs that assert:

- duplicate IDs, unknown keys, string commands, empty argv, invalid scopes, zero/negative timeouts, missing prerequisites, and prerequisite cycles fail before execution;
- `gatesPassed` is rejected;
- an argv element containing `$(touch /tmp/gate-injection-sentinel)` reaches a fixture command literally and never executes;
- a passing command writes `passed`, a nonzero command writes `failed`, and an unavailable prerequisite writes `not_run`;
- runner exit is nonzero when any selected required gate is `failed` or `not_run`;
- output paths cannot escape `--evidence-dir`.

Use a temporary Python fixture command in `tests/fixtures/gates/record_argv.py` rather than relying on platform-specific shell behavior.

- [ ] **Step 2: Run the tests and confirm the placeholder runner fails**

```bash
bash tests/operations/ci_gates_test.sh
bash tests/operations/accumulated_suite_test.sh
```

Expected: nonzero because no catalog executor exists and `gatesPassed` is present.

- [ ] **Step 3: Replace `ci/phase-gates.json` with explicit phase entries**

Use this entry shape:

```json
{
  "schemaVersion": 1,
  "gates": [
    {
      "id": "backend-unit",
      "phase": 1,
      "scope": "local",
      "required": true,
      "timeoutSeconds": 900,
      "command": ["podman", "run", "--rm", "-v", "./backend:/app:z", "-w", "/app", "docker.io/library/golang:1.26.5", "go", "test", "./..."],
      "prerequisites": [],
      "subjectKind": "source"
    }
  ]
}
```

Catalog all currently real local gates. Add later local/external IDs from the master plan with commands pointing to their fail-closed wrappers, never a fabricated pass. Set dependencies so certification depends on every required prior gate.

- [ ] **Step 4: Implement `scripts/run-gates.py`**

The runner must:

1. validate the complete catalog before selecting gates;
2. topologically order selected gates and prerequisites;
3. run from the repository root via `subprocess.run(argv, shell=False, timeout=gate["timeoutSeconds"])`;
4. pass only the inherited environment, without serializing it;
5. capture stdout/stderr in a mode-`0700` evidence directory and mode-`0600` log;
6. hash output bytes and the declared subject;
7. map exit `0` to `passed`, exit `3` or missing executable/prerequisite to `not_run`, and every other outcome to `failed`;
8. atomically write the envelope through `scripts/lib/evidence.py`;
9. stop dependent gates after a failure and emit `not_run` for each blocked dependent;
10. exit `0` only when every selected required gate is `passed`.

Do not add implicit retry. A rerun creates a new evidence directory; it never edits earlier evidence.

- [ ] **Step 5: Implement the compatibility wrapper**

Make `scripts/verify-accumulated-suite.sh` accept only:

```text
--candidate-lock PATH --evidence-dir PATH [--phase N] [--gate ID]
```

Translate the flags into a Python argv array and `exec python3 scripts/run-gates.py` with the parsed catalog, scope, candidate-lock, evidence-directory, phase, and repeated gate options. If neither phase nor gate is set, select all local gates.

- [ ] **Step 6: Run focused tests**

```bash
bash tests/operations/ci_gates_test.sh
bash tests/operations/accumulated_suite_test.sh
python3 -m py_compile scripts/run-gates.py tests/fixtures/gates/record_argv.py
```

Expected: all exit `0`, and the injection sentinel file is absent.

- [ ] **Step 7: Commit the gate runner**

```bash
git add ci/phase-gates.json scripts/run-gates.py scripts/verify-accumulated-suite.sh \
  tests/fixtures/gates/record_argv.py tests/operations/ci_gates_test.sh \
  tests/operations/accumulated_suite_test.sh
git commit -m "ci: execute beta gates from a validated catalog"
```

---

### Task 4: Establish a structural beta product contract

**Files:**
- Create: `documentation/product/beta-contract.json`
- Modify: `documentation/product/beta-feature-status.md`
- Modify: `scripts/check-product-truth.sh`
- Create: `tests/operations/product_truth_test.sh`

**Interfaces:**
- Produces: the canonical module/platform/architecture/removed-feature/gate inventory.
- Consumes: `frontend/src/components/AppShell.tsx`, `backend/main.go`, `docker-compose.yml`, `frontend/package.json`, `backend/go.mod`, and `ci/phase-gates.json`.

- [ ] **Step 1: Add product-contract mutation tests**

Create a test that copies the relevant files into a temporary tree and invokes `check-product-truth.sh --root "$fixture"`. Assert a clean copy passes, then independently mutate it to:

- remove `files` from the contract;
- add a fake `voice` navigation module;
- add a `livekit` Compose service;
- add `@livekit/components-react` to `package.json`;
- route the catch-all to `telos-core` while the contract says `telos-client`;
- remove a required gate ID;
- mark encrypted backups `shipping` before the recovery gate exists.

Every mutation must fail and name the violated contract field.

- [ ] **Step 2: Run the mutation test and capture the current false negatives**

```bash
bash tests/operations/product_truth_test.sh
```

Expected: nonzero because the current keyword scanner does not derive these structures.

- [ ] **Step 3: Add the machine-readable contract**

Create `documentation/product/beta-contract.json` with closed keys and exact values:

```json
{
  "schemaVersion": 1,
  "architecture": "standalone-client-server",
  "modules": ["chat", "stream", "library", "files"],
  "settingsSurface": "settings",
  "server": {"os": "ubuntu-24.04", "arch": "x86_64", "podmanMinimum": "5.0.0"},
  "desktop": ["linux-x86_64", "windows-x86_64", "macos-universal"],
  "browserService": "telos-client",
  "apiService": "telos-core",
  "removedFeatures": ["livekit", "voice-rooms", "watch-parties", "notification-inbox", "my-list"],
  "backupStatus": "blocked-until-operational-safety",
  "requiredExternalGates": [
    "clean-install", "browser-matrix", "desktop-linux", "desktop-windows",
    "desktop-macos", "separate-host-restore", "upgrade-rollback",
    "capacity", "host-exposure", "controlled-egress"
  ]
}
```

Use `settings` as a navigation surface but keep the community-content count at four modules in prose.

- [ ] **Step 4: Rewrite current beta status truthfully**

Update `documentation/product/beta-feature-status.md` to:

- name Chat, Stream, Library, and Files plus Settings;
- state the standalone hosted-browser and unsigned-desktop distribution boundary;
- mark audiobook catalog/playback only according to running code and existing tests;
- mark encrypted off-node backup/recovery as blocked until Phase 4 evidence exists;
- state platform-trusted HTTPS only;
- state the reference server and desktop/browser matrix;
- retain removed features as removed.

Do not claim a gate is shipping because its implementation plan exists.

- [ ] **Step 5: Extend the checker with parsers, not brittle line numbers**

Retain the current prohibited text/dependency checks, and use an embedded Python block or `scripts/lib/product_truth.py` to parse JSON/YAML-like source inventories. The checker must verify:

- the contract's modules match the `navItems` hrefs in `AppShell.tsx`;
- removed route prefixes are absent from `backend/main.go`;
- removed dependency names are absent from `frontend/package.json` and `backend/go.mod`;
- Compose contains required services and no removed services;
- Traefik service names match the contract;
- every required gate ID exists exactly once in `ci/phase-gates.json`;
- human beta status contains each supported platform and does not call a blocked capability shipping.

Accept `--root PATH` solely for mutation fixtures; default to `git rev-parse --show-toplevel`.

- [ ] **Step 6: Run product truth tests**

```bash
bash tests/operations/product_truth_test.sh
bash scripts/check-product-truth.sh
```

Expected: both exit `0`; each fixture mutation fails for its intended reason.

- [ ] **Step 7: Commit the product contract**

```bash
git add documentation/product/beta-contract.json documentation/product/beta-feature-status.md \
  scripts/check-product-truth.sh tests/operations/product_truth_test.sh
git commit -m "docs: make the standalone beta contract executable"
```

---

### Task 5: Remove current removed-feature and operator-path residue

**Files:**
- Modify: `frontend/dev-server.mjs`
- Modify: `README.md`
- Modify: `CLAUDE.md`
- Modify: `documentation/README.md`
- Modify: `documentation/operations/backup-and-restore.md`
- Modify: `documentation/operations/beta-certification.md`
- Modify: `documentation/operations/browser-certification.md`
- Modify: `documentation/operations/clean-checkout-verification.md`
- Modify: `documentation/operations/container-hardening.md`
- Modify: `documentation/operations/controlled-egress.md`
- Modify: `documentation/operations/network-exposure.md`
- Modify: `documentation/operations/release.md`
- Modify: `documentation/operations/release-inputs.pathspec`
- Modify: `scripts/provision-jellyfin.sh`
- Modify: `scripts/provision-grimmory.sh`
- Modify: `tests/operations/product_truth_test.sh`

**Interfaces:**
- Consumes: contract from Task 4.
- Produces: no LiveKit dev proxy and no supported-product references to removed functionality.

- [ ] **Step 1: Add dev-proxy and CLI truth assertions**

Extend `product_truth_test.sh` to assert that `frontend/dev-server.mjs` has no `TELOS_DEV_LIVEKIT_URL`, `/livekit`, or LiveKit status log; current CLI help names the Bash implementation; and the legacy provisioning scripts direct operators to `telos configure integrations` while exiting `3`.

- [ ] **Step 2: Run the focused test and confirm it catches `frontend/dev-server.mjs`**

```bash
bash tests/operations/product_truth_test.sh
```

Expected: nonzero with the LiveKit target and route reported.

- [ ] **Step 3: Remove the LiveKit proxy branch**

Delete `livekitTarget`, the `/livekit` branch in `routeRequest`, and the final LiveKit log line. Preserve API/WebSocket proxying to the gateway and ordinary requests to Next.js.

- [ ] **Step 4: Reconcile current documentation and CLI help**

Run the checker, address only current shipped/operations surfaces it reports, and preserve historical records under `docs/superpowers/`. Verify that generated `telos help` already presents the Bash dispatcher as authoritative; label `backend/cmd/telos/` experimental in current documentation wherever it is mentioned. Make the two provisioning wrappers print the exact guided command and remain `not_run` until Phase 2 supplies it.

- [ ] **Step 5: Run source and documentation checks**

```bash
node --check frontend/dev-server.mjs
bash tests/operations/product_truth_test.sh
bash scripts/check-product-truth.sh
bash scripts/check-release-truth.sh
grep -rn "ci""te:" documentation/ AGENTS.md
grep -rnE "TO""DO|TB""D" documentation/ AGENTS.md
```

Expected: all checks exit `0`; both grep commands print nothing.

- [ ] **Step 6: Commit truth cleanup**

```bash
git add frontend/dev-server.mjs README.md CLAUDE.md documentation \
  scripts/provision-jellyfin.sh scripts/provision-grimmory.sh \
  tests/operations/product_truth_test.sh
git commit -m "chore: remove stale beta product claims"
```

---

### Task 6: Wire Phase 1 into CI and prove the phase exit

**Files:**
- Modify: `.github/workflows/ci.yml`
- Modify: `ci/phase-gates.json`
- Modify: `tests/operations/ci_gates_test.sh`
- Modify: `scripts/verify-clean-checkout.sh` — add the new runtime/release-critical Phase 1 inputs to its explicit inventory.

**Interfaces:**
- Produces: required CI jobs for evidence, gate-catalog, product-truth, and operations behavioral tests.
- Consumes: all Phase 1 outputs.

- [ ] **Step 1: Add a CI workflow contract test**

Extend `tests/operations/ci_gates_test.sh` to parse `.github/workflows/ci.yml` and require steps for:

```text
tests/operations/evidence_envelope_test.sh
tests/operations/beta_certification_test.sh
tests/operations/ci_gates_test.sh
tests/operations/accumulated_suite_test.sh
tests/operations/product_truth_test.sh
scripts/check-product-truth.sh
scripts/check-release-truth.sh
scripts/verify-clean-checkout.sh --inventory-only
```

Assert each step is a required job step without `continue-on-error: true`.

- [ ] **Step 2: Run the test and observe the workflow gap**

```bash
bash tests/operations/ci_gates_test.sh
```

Expected: nonzero listing missing workflow commands.

- [ ] **Step 3: Add a `truth-and-gates` CI job**

Use Ubuntu, `actions/checkout` pinned to the same policy as existing jobs, least-privilege `contents: read`, and standard repository tools. Run the eight commands above. Do not upload a `passed` candidate envelope because CI does not yet have a frozen candidate lock; upload raw test logs only on failure if existing workflow policy allows it.

- [ ] **Step 4: Add Phase 1 files to clean-checkout inventory**

Ensure `scripts/lib/evidence.py`, `scripts/not-run.py`, `scripts/run-gates.py`, `documentation/product/beta-contract.json`, and the evidence schema are classified as runtime/release-critical and must be tracked.

- [ ] **Step 5: Run the Phase 1 exit suite**

```bash
bash tests/operations/evidence_envelope_test.sh
bash tests/operations/beta_certification_test.sh
bash tests/operations/ci_gates_test.sh
bash tests/operations/accumulated_suite_test.sh
bash tests/operations/product_truth_test.sh
bash scripts/check-product-truth.sh
bash scripts/check-release-truth.sh
bash scripts/verify-clean-checkout.sh --inventory-only
python3 -m py_compile scripts/lib/evidence.py scripts/not-run.py scripts/run-gates.py
```

Expected: every command exits `0`. The later-phase wrappers still exit `3` when invoked normally, which is the correct Phase 1 state.

- [ ] **Step 6: Commit CI enforcement**

```bash
git add .github/workflows/ci.yml ci/phase-gates.json tests/operations/ci_gates_test.sh \
  scripts/verify-clean-checkout.sh
git commit -m "ci: enforce product and gate truth"
```

- [ ] **Step 7: Record the phase review checkpoint**

From a clean worktree, run:

```bash
git status --short
git log --oneline --decorate -6
```

Expected: empty status and six independently reviewable Phase 1 commits. Request code review before beginning Phase 2.
