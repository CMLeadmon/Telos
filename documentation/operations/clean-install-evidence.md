# Clean-install evidence (the `clean-install` gate)

This runbook produces the evidence that a **fresh Ubuntu 26.04 x86-64 host** (the verified reference release) installs,
configures and serves all four Telos modules in a browser over platform-trusted HTTPS.
It is the only way the `clean-install` gate can pass. The gate observes a real node;
nothing in this repository can pass it by itself.

## Current status

| Item | Status |
|---|---|
| Drill, collector, evaluator and result contract | Implemented and covered by hermetic tests (`tests/operations/clean_install_drill_test.py`). |
| Run against a real Ubuntu 24.04 node with public DNS and TLS | **Not run.** No such host was available to the implementer. |
| Collector output against a real Podman 5 | **Unverified.** `node_observe.py` parses `podman inspect` / `podman volume ls` / `ss` output shaped as documented; the shape was not captured from a live Podman 5 host. The first live run must confirm it (a parse failure fails closed). |
| Podman 5 source | **Verified on Ubuntu 26.04** (archive Podman 5.7.0 + the pinned podman-compose 1.6.0 wheel, on a 26.04.1 KVM VM). **Unavailable for 24.04** — no verified recipe; see [install.md](install.md). |

A rehearsal record (`recordKind: rehearsal`) can prove the procedure works on a
rehearsal host. It can never satisfy the gate: final import rejects any rehearsal
record, and the `clean-install` gate must be re-run against the **frozen candidate's**
artifacts in Phase 5.

## Prerequisite validation (do this once, before the first live attempt)

Podman 5 and the Compose provider are prerequisites the repository cannot assume.
On a *disposable* Ubuntu 26.04 VM (for 24.04, only once a source exists), record and verify:

1. The exact package or source URL, version and signing key or checksum of the Podman
   5+ installation, and the commands that install it. Compare with the
   [Ubuntu package index](https://packages.ubuntu.com/search?keywords=podman&searchon=names&suite=noble)
   and [Podman's instructions](https://podman.io/docs/installation); do not assume
   `apt install podman` supplies major version 5 and do not mix distribution repositories.
2. The `podman-compose` 1.6.0 installation ([install.md](install.md) lists the pinned wheel
   hash), then that `sudo` Podman can: render this repository's Compose files, run with
   stdin (`run -T`), mount read-only, create the four networks, and enforce resource limits.
3. `./install.sh --check` exits 0 and prints no unverified-combination warning.

If no source can be verified, leave the prerequisite **unavailable**: do not write a recipe,
do not lower the Podman floor, and do not run the live drill. Record the gap in the
implementation ledger. Local development continues; the gate stays `not_run`.

## What the live attempt needs

- A newly provisioned Ubuntu 26.04 x86-64 node (**no** pre-existing Telos volume, container
  or `/var/lib/telos`; **no** reused evidence directory — either one invalidates the run),
  with the verified Podman and provider above.
- A DNS name pointing at the node and **inbound 80 and 443 reachable from the internet**
  (Let's Encrypt HTTP-01 cannot be proven with a loopback `curl`).
- A **second machine**, the *verifier*, that runs the drill. The drill refuses to claim
  reachability "from outside the node" when it runs on the node itself.
- SSH from the verifier to the node: a key, a protected SSH config, a pinned `known_hosts`
  entry, and a user that is root or has passwordless `sudo -n` (the collector reads
  root-only state and rootful Podman).
- Legal media fixtures installed in the libraries: one video, one audio item, one ebook,
  and a chat channel, described by a fixture manifest.
- Owner and member credentials (and the bootstrap token for an empty node).

## Procedure

Define the node's identity once. `hostId` is the SHA-256 of the raw bytes of
`/etc/machine-id` (the raw machine id is never published):

```bash
printf 'sha256:%s\n' "$(sha256sum /etc/machine-id | cut -d' ' -f1)"      # run on the node
```

**1. Record the clean precondition (on the node, before installing).**

```bash
sudo python3 -I scripts/lib/node_observe.py clean | tee clean-precondition.json
```

Expect no `/var/lib/telos`, `/srv/telos` or `/etc/telos`, and no Telos container or volume.
Keep the file with the run's notes.

**2. Install** by following [install.md](install.md) exactly: `install.sh`, `telos init`,
`telos configure integrations --setup`, `telos start`.

**3. Watch the setup window.** While `telos configure integrations --setup` is running,
in a second terminal on the node:

```bash
sudo python3 -I scripts/lib/node_observe.py watch-setup /var/lib/telos 1800 2
```

It polls every 2 s for up to 30 minutes, records every host binding the Jellyfin and
Grimmory containers publish, and stores the result under `/var/lib/telos/state/`
(root, `0600`). The drill requires every binding to be `127.0.0.1` and requires both
services to have been seen. Polling can miss a binding that lasts less than the interval;
the setup overlay itself pins loopback, so this is a corroborating observation.

**4. Describe the target** on the verifier, outside the checkout, mode `0600`
(`release/drill-target.schema.json` is the closed schema):

```json
{
  "schemaVersion": 1,
  "origin": "https://node.example.net",
  "nodePath": "/var/lib/telos",
  "hostId": "sha256:…",
  "sshTarget": "ops@node.example.net",
  "sshConfigFile": "/home/verifier/.telos-drill/ssh_config",
  "credentialsFile": "/home/verifier/.telos-drill/credentials.json",
  "restoreTargetFile": null,
  "predecessorGeneration": null,
  "capacityTargetFile": null,
  "confirmation": null
}
```

The SSH config must enable strict host-key checking against a pinned `known_hosts`.
`credentials.json` (mode `0600`) is `{"owner":{"username","password"},"member":{"username","password"}}`
plus `"bootstrapToken"` for an empty node; it is passed to the journey probe on stdin and is
never copied into evidence. The fixture manifest is described in
`scripts/lib/browser_node_smoke.py`.

**5. Run the drill from the verifier.** As a source-bound rehearsal:

```bash
bash scripts/drills/clean-node-install.sh --target-file /secure/target.json \
  --fixture-manifest /secure/fixtures.json --rehearsal --source-commit "$(git rev-parse HEAD)" \
  --result-out out/result.json        # `out/` must exist and be empty
```

For candidate evidence, the runner invokes the same producer with the frozen candidate lock
and a gate-input manifest (`{"gateInputs":{"clean-install":{"targetFile":"…","fixtureManifest":"…"}}}`):

```bash
python3 scripts/run-gates.py --catalog ci/phase-gates.json --scope external \
  --candidate-lock /path/to/candidate-lock.json --evidence-dir /new/empty/dir \
  --gate clean-install --inputs /secure/gate-inputs.json
```

Exit codes: `0` passed, `1` failed, `2` unsafe or malformed input (nothing was done),
`3` not_run. The drill verifies the node's machine identity **before** collecting anything;
a wrong host stops with exit 2.

## What the drill asserts

| Assertion | Meaning |
|---|---|
| `host.platform` | Ubuntu ≥ 24.04, x86-64 (any release other than the verified 26.04 reference is noted as an unverified combination) |
| `runtime.podman`, `runtime.provider` | rootful Podman ≥ 5.0.0; `podman-compose` 1.6.0 at an absolute path |
| `node.path`, `node.env`, `node.secrets`, `node.libraries` | `/var/lib/telos` root-owned under root-owned, non-writable, non-symlink ancestors; `node.env` root `0600`; every production secret set and not a placeholder (values are never read); `TELOS_ENV=production` with Jellyfin and Grimmory library IDs |
| `node.signing`, `node.state` | `config/` is `10001:10001` `0700` holding exactly the cursor and HLS files (`0600`, non-trivial size); `state/` root `0700` |
| `services.running` | all ten long-running services running; `telos-migrate` exited 0; no unexpected Telos container |
| `ports.published`, `ports.listeners` | only Traefik publishes host ports, only 80 and 443; no internal service port listens on a non-loopback address |
| `client.isolation`, `core.identity` | `telos-client` runs as 101, attached only to `telos-ingress`, no bind/volume mounts, no ports; `telos-core` runs as `10001:10001` |
| `clean.provenance` | no Telos volume or container predates `node.env` (uses the filesystem birth time; `not_run` if unavailable) |
| `setup.loopback` | every setup binding observed was on `127.0.0.1` (`not_run` if no setup window was watched) |
| `tls.chain`, `public.http-redirect` | the certificate verifies against the verifier's platform trust store, is not self-signed, and has ≥ 14 days left; port 80 redirects to the same host over HTTPS |
| `journey.smoke` | the member journey in `scripts/smoke-browser-node.sh`: owner/invite flow, chat, file upload and comment, real video/audio/ebook reads, and independent Jellyfin and Grimmory outage degradation |

Any `failed` assertion fails the gate; any `not_run` leaves it `not_run`. A pass requires
all of them. The result carries `observation.json` (sanitized facts: modes, owners, versions,
container names, published ports, key *names* — never values), `assertions.json`,
`journey-report.json` and, only on a pass, the `external-host` subject descriptor that names
the node by hashed machine identity.

## If something is unavailable

Record the missing prerequisite (no DNS, no inbound 80/443, no second host, no media fixtures,
no verified Podman) in `docs/implementation/standalone-beta-progress.md` and leave the gate
`not_run`. Do not substitute a loopback run, a mocked suite, or an empty library.
