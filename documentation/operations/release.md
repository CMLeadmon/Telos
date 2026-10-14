# Telos Release Engineering and Verification Guide

> **Status.** The release builder (`scripts/build-release.sh`), the derived build-free Compose model, the candidate
> installer (`scripts/install-candidate.sh`) and the generation upgrade/rollback machinery are implemented and tested
> against stand-in container tools and a miniature repository (`tests/operations/release_test.sh`). Rehearsal
> builds on the reference VMs are recorded in `docs/implementation/standalone-beta-progress.md`. A **candidate** build is still impossible: `release/telos-tag-signers` enrolls no
> signer, so there is no tag the builder may trust and it exits 3 (`not_run`). Signing, staging and publishing are
> described below as intended; their scripts report `not_run` until Phase 5 supplies real keys and a publisher
> identity. Rehearsal output can never be certification evidence.

## Non-circular hash graph

```
payload bytes + artifact-bound SBOMs -> artifact-index.json -> release-manifest.json -> candidate-lock.json
   -> gate results / envelopes -> evidence-index.json -> detached signatures and certification
```

The authoritative description (field sets, digest forms, exclusions) is `release/hash-graph.md` and the closed
schemas beside it; `scripts/lib/release_graph.py` is the one serializer and validator. `SHA256SUMS` lists every
payload file but not itself; nothing downstream of the candidate lock (evidence, scans, signatures, the installed
receipt) is ever inside the candidate.

## Building a candidate

```bash
# a candidate: signed annotated tag, clean checkout of exactly that tag, native desktop artifact from the same run
scripts/build-release.sh --tag v0.1.0-beta.N --version 0.1.0-beta.N --desktop-dir DIR --out-dir /abs/out --verify-reproducible

# a rehearsal: any committed state; recorded as a rehearsal in build-record.json and never usable as a candidate
scripts/build-release.sh --rehearsal --source-commit <40-hex> --version 0.1.0-beta.N --out-dir /abs/out [--desktop-dir DIR]
```

`--out-dir` must be absolute, outside the repository, and empty. The builder (`scripts/lib/build_release.py`):

1. resolves the source. A candidate needs an annotated tag `v<version>` verified against `release/telos-tag-signers` (no
   enrolled signer is `not_run`, a lightweight tag or one signed by anyone else is refused), `HEAD` on that commit and a
   clean working tree. A rehearsal needs the full commit.
2. exports exactly that commit with `git archive`, requires `frontend/package.json`, `tauri.conf.json` and `Cargo.toml` to
   declare the version, and runs the image-lock check inside the export.
3. builds the three images Telos owns (`telos-core`, `telos-client`, `telos-egress-proxy`) with the locked bases,
   `podman build --timestamp $SOURCE_DATE_EPOCH` and version/source labels, and archives them as OCI archives.
   Third-party images are archived with `skopeo copy --preserve-digests` from their locked linux/amd64 platform manifest,
   and the archived manifest must equal that locked digest — so a loaded image resolves by the upstream digest with no registry.
4. verifies the native desktop artifact with `scripts/verify-desktop-artifact.py` (never executed) and its identity
   sidecar (commit and version must match).
5. assembles the generation (below), writes artifact-bound SPDX SBOMs with the locked Syft (creation time and namespace
   normalized from `SOURCE_DATE_EPOCH`), then artifact index → release manifest → candidate lock → `SHA256SUMS`, and verifies the graph.
6. packages `telos-<version>-candidate.tar.zst` (sorted, root-owned, fixed times, single-threaded zstd) with its detached
   `.sha256`, re-extracts it and requires the same candidate-lock digest. `build-record.json` (outside the candidate) says
   whether this was a `candidate` or a `rehearsal`.

With `--verify-reproducible` it builds twice in separate scratch directories, the second without cache, and refuses unless
every file of the two candidates is byte-identical. The candidate lock digest exists only after step 5; nothing built earlier mentions it.

### Payload layout

| Path | Kind | Notes |
|---|---|---|
| `source/telos-<version>-source.tar` | source | the `git archive` of the commit |
| `images/<name>.oci.tar` | oci-image | `contentDigest` is the OCI manifest digest |
| `desktop/Telos_<version>_amd64.AppImage` + `.identity.json` | desktop | unsigned Linux x86-64 |
| `generation/**` | generation | the operational tree, below |
| `sbom/*.spdx.json`, `sbom/sbom-index.json` | sbom | one SBOM per image and desktop artifact |
| `artifact-index.json`, `release-manifest.json`, `candidate-lock.json`, `SHA256SUMS` | graph | |

### The generation tree

Tracked operational files only: `telos`, `cli/`, `scripts/` (not `scripts/tests`), `config/`, `deploy/` (not Dockerfiles),
`release/`, `documentation/`, `backend/db/migrations/`, the licence files and `.env.example`. Symlinks, `.env*`, keys and
secret-looking content are refused. Three files are generated:

- `docker-compose.yml` and `docker-compose.setup.yml` — **derived**: every `build:` block removed and every third-party
  image rewritten to `repository@<locked platform digest>` (`scripts/lib/release_compose.py`). podman-compose 1.6.0 cannot
  remove a `build:` key with an override (`!reset` crashes it), so a derived base is the only way to guarantee nothing builds.
- `docker-compose.release.yml` — gives the self-built services `${TELOS_CORE_IMAGE:?…}`, `${TELOS_CLIENT_IMAGE:?…}` and
  `${TELOS_EGRESS_PROXY_IMAGE:?…}`; Compose refuses to start them without those variables.
- `images.env` (mode 0600, no secret) — the three variables as `localhost/<name>@sha256:<manifest digest>`.
- `generation.json` — version, source commit, image manifest digests, the migration window, `predecessorIds` and
  `rollbackCompatibleThrough` (from `release/generation-compat.json`, conservative by default), and the SHA-256 of every
  other file in the tree.

Inside a generation the CLI sees `images.env` beside the overlay, layers the overlay, exports the three variables from the
strictly parsed file and refuses `telos start --build`.

## Installing a candidate

```bash
zstd -d telos-<version>-candidate.tar.zst -c | tar -x -C /root/candidate         # after checking the .sha256
sudo scripts/install-candidate.sh --candidate /root/candidate --activate       # first install only
```

`scripts/install-candidate.sh` verifies the whole graph, checks every OCI archive against the index, `podman load`s it and
proves every image the generation will run resolves **by digest from local storage**, then copies `generation/` to
`/srv/telos/generations/<candidate-lock digest>` (root-owned, mode 0755, never overwritten) and verifies it against its own
`generation.json`. The candidate lock and graph documents are kept in `/var/lib/telos/state/candidates/<digest>/`.
`--activate` is for a node with no active generation: it points `/srv/telos/current` at it and writes the installed receipt
and `state/current-candidate-lock.json` (the lock the backup unit names). Moving a running node to another generation is
`scripts/upgrade.sh`; `telos` itself is then `/srv/telos/current/telos`.

## Generations on a node

An installed release is an immutable *generation* under `/srv/telos/generations/<candidate digest hex>/`, selected by
the single mutable symlink `/srv/telos/current`; mutable state (`node.env`, signing keys, journals, the installed
receipt) lives in `/var/lib/telos`, never in a generation. `generation.json` is candidate-independent: it carries no
candidate digest, install path or time, and it does not hash itself, so it can be built before the candidate lock exists.
The installed receipt (`state/installed-generation.json`) binds the candidate-lock digest to the manifest digest after
verification. Upgrading and rolling back between generations is described in
[`upgrade-and-rollback.md`](./upgrade-and-rollback.md); `release/predecessor-policy.json` names the exact earlier
release (tag, commit, candidate-lock digest, generation-manifest digest) an upgrade is certified from — or `null` while
none exists, in which case `scripts/build-predecessor.sh` reports `not_run`.
