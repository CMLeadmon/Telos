# Standalone Beta Phase 2: Clean Linux Deployment Implementation Plan

> **Remediated 2026-10-13:** Ubuntu 24.04+ x86-64 server; browser delivery first. This is an active execution plan. Read the [cold handoff](2026-10-13-standalone-beta-cold-handoff.md) and [shared contracts](2026-10-13-standalone-beta-handoff-contracts.md) before implementation; they own runtime identity, rehearsal/evidence, and candidate interfaces.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a fresh Ubuntu 24.04 LTS x86-64 host install, configure, and serve all four Telos modules in a browser through the authoritative Bash CLI and platform-trusted HTTPS.

**Architecture:** Build the static Next.js export into a separate unprivileged `telos-client` service connected only to the ingress network. Keep every API route on the headless Go gateway and route the Traefik catch-all to the client. Generate signing files during one-time initialization, mount them read-only, and finish third-party setup through a loopback-only Compose overlay plus a container-network validation command. Refuse incomplete or destructive starts.

**Tech Stack:** Bash `telos` CLI, Podman 5+, Compose, Go 1.26.5, Next.js 16.2.10 static export, nginx unprivileged Alpine runtime, Traefik, Jellyfin HTTP API, Grimmory HTTP API, Playwright 1.61.1.

**Spec:** `docs/superpowers/specs/2026-08-20-standalone-beta-readiness-design.md`

## Global Constraints

- Execute after Phase 1 local implementation is merged. Missing reference infrastructure is recorded as `not_run`; it blocks final certification, not independent Phase 3/4 implementation. Phase 5 Task 1 schema and Task 4a runner/evidence support can be developed early; no frozen candidate is required here.
- Production architecture is the headless `telos-core` service plus `telos-client`; do not enable `EMBED_FRONTEND` in production Compose.
- `telos-client` has only `telos-ingress`, no host ports, no secrets, no volumes containing member data, and no access to `telos-backend` or `telos-db`.
- Traefik is the only public listener. Browser API/WebSocket calls remain same-origin.
- `telos init` never overwrites `node.env`, signing files, storage, or durable volumes. Production uses rootful Podman through `sudo telos`, `TELOS_NODE_PATH=/var/lib/telos`, root-owned `node.env` mode 0600, and `config/` owned by gateway UID/GID 10001 with directory 0700/files 0600. The node path is outside immutable release trees; rootless development defaults to `$REPO_ROOT/.telos`. Remove `--force` and every instruction recommending it.
- Production start requires non-placeholder upstream credentials, at least one stable Jellyfin library ID, at least one stable Grimmory library ID, and readable, mode-correct signing files.
- Guided setup exposes Jellyfin and Grimmory only on `127.0.0.1`. A remote operator uses an SSH tunnel; no setup port may bind `0.0.0.0` or a public interface.
- Secret prompts disable terminal echo. Secrets travel over a dedicated inherited file descriptor or Compose environment interpolation, never command-line arguments, a process environment created only for JSON encoding, log output, evidence, or temporary world-readable files.
- The backend validation mode talks to upstreams only over their HTTP APIs. Preserve the copyleft process boundary.
- Do not edit any applied migration; this phase requires no schema migration.
- Keep Phase 1's evidence/gate interfaces stable.

---

## Execution and review focus

Run command blocks from the repository root unless the block explicitly changes directory; each block starts with a fresh shell. The task numbers are stable cross-references. Within Tasks 3, 5, and 6, complete the parser/command/unit deliverable before the container or live-host deliverable and review each separately.

- Interrupted initialization must not overwrite a node or adopt a directory it cannot prove it created (Task 3).
- Compose interpolation must preserve secrets containing `$`, `#`, spaces, quotes, and backslashes (Tasks 3/5).
- First setup must build its validation image before use and remove loopback listeners even on interruption (Task 5).
- A passing mocked browser suite or empty media library cannot stand in for live reading/playback (Task 6).
- A clean Ubuntu 24.04 machine must have a verified Podman 5+ source, consistent runtime identity, and reachable public 80/443 (Task 7).

## Current-State Baseline

The server image defaults to headless mode (`backend/Dockerfile:1-25`), its non-embedded frontend registration is empty (`backend/frontend_noembed.go:7-9`), and production Compose selects that default build (`docker-compose.yml:114-118`). The current Traefik catch-all still sends browser paths to `telos-core` (`config/dynamic/routes.yaml:55-66`). Signing-file paths exist in the environment inventory (`.env.example:33-41`), but `telos-core` receives only those strings and has no matching secret-file mount (`docker-compose.yml:134-161`). Production startup also rejects placeholder upstream credentials and missing library IDs (`backend/main.go:345-367`, `backend/main.go:613-628`). This phase closes those concrete installation gaps.

---

## File Structure

- Create: `frontend/Dockerfile` — reproducible static export and unprivileged runtime image.
- Create: `frontend/.dockerignore` — exclude dependencies, caches, native targets, local configuration, and generated output from the client build context.
- Create: `frontend/nginx.conf` — static routing, immutable asset caching, SPA/static-export fallback, and health endpoint.
- Create: `tests/operations/static_client_test.sh` — image-content, identity, and network contract tests.
- Modify: `docker-compose.yml` — add `telos-client`, signing-file mount, and required environment.
- Modify: `config/dynamic/routes.yaml` — route API to core and catch-all to client.
- Modify: `scripts/validate-compose.sh` — standalone routing/network/secret-mount assertions.
- Modify: `tests/operations/runtime_hardening_test.sh` — static Compose assertions owned by this phase.
- Modify: `.env.example` — template only; host config path and supported Tauri origins input.
- Modify: `.gitignore` — ignore generated node configuration.
- Modify: `cli/commands/init.sh` — safe key generation and no overwrite path.
- Modify: `cli/commands/doctor.sh` — granular diagnosis and non-destructive repairs.
- Modify: `cli/commands/start.sh` — preflight completeness refusal.
- Create: `cli/lib/config.sh` and `cli/lib/node_config.py` — resolve the persistent node path, parse/write `node.env` and validate signing-file inputs without sourcing it. Audit all `cli/commands/*.sh`, `cli/lib/common.sh`, and `install.sh` for direct checkout `.env` reads and route them through the resolver.
- Create: `tests/operations/node_config_test.sh` — init/doctor/start behavior tests.
- Create: `backend/integration_config.go` — upstream discovery and command-mode JSON contract.
- Create: `backend/integration_config_test.go` — `httptest` discovery tests and secret-redaction assertions.
- Modify: `backend/main.go` — dispatch `validate-integrations` before normal startup.
- Create: `docker-compose.setup.yml` — loopback-only first-run overlay.
- Create: `cli/commands/configure.sh` — `telos configure integrations` guided workflow.
- Create: `tests/operations/configure_integrations_test.sh` — prompt, selection, atomic write, and failure tests.
- Create: `scripts/smoke-browser-node.sh` — external HTTPS and member-journey probe.
- Create: `tests/operations/browser_node_smoke_test.sh` — deterministic fixture server tests.
- Modify: `tests/operations/install_test.sh`, `install.sh`, `cli/lib/preflight.sh` — clean Ubuntu reference-host contract and actual prerequisite enforcement.
- Modify: `documentation/operations/install.md` — exact reference-host guided installation.
- Create: `documentation/operations/guided-integrations.md` — Jellyfin/Grimmory manual setup and validation.
- Modify: `ci/phase-gates.json`, `.github/workflows/ci.yml` — Phase 2 local gates.
- Modify: `frontend/AGENTS.md` — reconcile the durable frontend guide with same-origin delivery from the standalone client container and remote token mode in Tauri.

---

### Task 1: Build an isolated static-client image

**Files:**
- Create: `frontend/Dockerfile`
- Create: `frontend/.dockerignore`
- Create: `frontend/nginx.conf`
- Create: `tests/operations/static_client_test.sh`

**Interfaces:**
- Produces: an OCI image serving `/out` on container port `8080` as a non-root user.
- Produces: `GET /healthz` returns `200` without touching the Go gateway.
- Consumes later: `telos-client` Compose service and release image lock.

- [ ] **Step 1: Add a failing static-client image contract test**

Create `tests/operations/static_client_test.sh`. Build a temporary tag, run it with `--read-only --cap-drop=all --security-opt=no-new-privileges --tmpfs /var/cache/nginx --tmpfs /var/run`, and assert:

```bash
image="localhost/telos-client-phase2:$RANDOM"
podman build -f frontend/Dockerfile -t "$image" frontend
cid="$(podman run -d --read-only --cap-drop=all --security-opt=no-new-privileges \
  --tmpfs /var/cache/nginx --tmpfs /var/run -p 127.0.0.1::8080 "$image")"
trap 'podman rm -f "$cid" >/dev/null 2>&1 || true; podman rmi "$image" >/dev/null 2>&1 || true' EXIT

uid="$(podman exec "$cid" id -u)"
[[ "$uid" != 0 ]]
port="$(podman port "$cid" 8080/tcp | sed 's/.*://')"
curl -fsS "http://127.0.0.1:$port/healthz" | grep -qx 'ok'
root_html="$(curl -fsS "http://127.0.0.1:$port/")"
grep -q '<html' <<<"$root_html"
asset_path="$(grep -oE '/_next/static/[A-Za-z0-9._/?=&%-]+' <<<"$root_html" | head -n 1)"
[[ -n "$asset_path" ]]
curl -fsSI "http://127.0.0.1:$port$asset_path" | grep -qi 'cache-control:.*immutable'
```

Use a bounded readiness loop instead of a sleep. Inspect both image history and the exported final root filesystem; a multi-stage build history alone does not prove absence of files. Reject `.env`, `node.env`, `telos-secrets.json`, source maps, `node_modules`, and copied build sources under the application paths. The nginx base image includes Alpine system tools; do not mistake its package database or shell for a copied Node build stage. Add explicit `/login/`, `/stream/`, missing-path 404, and HTML no-store assertions. Give the test tmpfs mounts `uid=101,gid=101,mode=0750`, matching Compose, so the unprivileged worker can write them.

- [ ] **Step 2: Run the test and observe the missing Dockerfile**

```bash
bash tests/operations/static_client_test.sh
```

Expected: nonzero because `frontend/Dockerfile` does not exist.

- [ ] **Step 3: Implement the two-stage client image**

Create `frontend/.dockerignore` first with `.next`, `out`, `node_modules`, `src-tauri/target`, `.env*`, test results, Playwright reports, and OS/editor files excluded. Then create `frontend/Dockerfile` with exact build behavior:

```dockerfile
FROM node:24.18.0-alpine AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . ./
RUN npm run build

FROM nginxinc/nginx-unprivileged:1.27-alpine
COPY nginx.conf /etc/nginx/nginx.conf
COPY --from=build --chown=101:101 /app/out /usr/share/nginx/html
EXPOSE 8080
USER 101:101
```

Phase 4 replaces tags with reviewed digests. Do not add runtime Node, npm, application source, or shell-written runtime configuration. The nginx Alpine base contains system utilities. Override the inherited entrypoint with `ENTRYPOINT ["nginx", "-g", "daemon off;"]` and `CMD []` so its configuration-writing startup scripts do not run against a read-only filesystem.

- [ ] **Step 4: Configure static serving**

`frontend/nginx.conf` must run without a writable root, place PID/cache/temp paths under `/var/run` and `/var/cache/nginx`, listen on `8080`, return plain `ok` from `/healthz`, set a one-year immutable cache only for hashed `/_next/static/` files, set `no-store` for HTML, return existing exported `.html`/directory files, and use `/404.html` with status `404` for unknown paths. Do not return `index.html` with `200` for arbitrary nonexistent API paths.

- [ ] **Step 5: Run focused tests**

```bash
bash tests/operations/static_client_test.sh
cd frontend && npm run build && cd ..
```

Expected: both exit `0`; the container runs as UID 101 and `/healthz` is independent.

- [ ] **Step 6: Commit the static client**

```bash
git add frontend/Dockerfile frontend/.dockerignore frontend/nginx.conf \
  tests/operations/static_client_test.sh
git commit -m "frontend: add standalone static client image"
```

---

### Task 2: Route browser and API traffic to separate services

**Files:**
- Modify: `docker-compose.yml`
- Modify: `config/dynamic/routes.yaml`
- Modify: `scripts/validate-compose.sh`
- Modify: `tests/operations/runtime_hardening_test.sh`
- Modify: `tests/fixtures/compose.env` — add the new non-secret path/origin fixture values.

**Interfaces:**
- Consumes: `frontend/Dockerfile` and `GET /healthz` from Task 1.
- Produces: `telos-client` on `telos-ingress`; Traefik `telos-web` catch-all; all `/api/v1/*` routers remain `telos-core`.

- [ ] **Step 1: Add standalone topology checks**

Extend `scripts/validate-compose.sh` with `standalone-client` and `route-split` checks. Extend the existing `--checks` option allowlist/help and fixture harness alongside the implementation, so an unknown-check error cannot masquerade as the expected failing topology test. Parse rendered Compose and `config/dynamic/routes.yaml` and assert:

- `telos-client` exists, builds `frontend/Dockerfile`, has only `telos-ingress`, publishes no ports, mounts no volumes, receives no environment variable containing `PASSWORD`, `TOKEN`, `SECRET`, `DATABASE`, `REDIS`, `STORAGE`, `JELLYFIN`, or `GRIMMORY`, and has a `/healthz` health check;
- `telos-core` still uses the default headless target and joins all three required networks;
- every API-specific router targets `telos-core`; for representative known and unknown `/api/v1/` requests, the highest-priority matching router targets core (the priority-1 web catch-all also matches, so do not reject it for that alone);
- the priority-1 catch-all targets `telos-client`;
- a `telos-client` Traefik service resolves to `http://telos-client:8080`;
- neither core nor client publishes a host port.

Add fixture mutations to `runtime_hardening_test.sh` for an accidental backend network, a published client port, a client secret, an API-to-client route, and a catch-all-to-core route.

- [ ] **Step 2: Run the checks and confirm current topology fails**

```bash
bash scripts/validate-compose.sh --env-file tests/fixtures/compose.env --checks standalone-client,route-split
bash tests/operations/runtime_hardening_test.sh
```

Expected: nonzero because `telos-client` is absent and the catch-all targets core.

- [ ] **Step 3: Add `telos-client` to Compose**

Use this service boundary, retaining exact project conventions for labels and health syntax:

```yaml
  telos-client:
    build:
      context: ./frontend
      dockerfile: Dockerfile
    container_name: telos-client
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    read_only: true
    tmpfs:
      - /var/cache/nginx:size=16m,mode=0750,uid=101,gid=101
      - /var/run:size=1m,mode=0750,uid=101,gid=101
    networks:
      - telos-ingress
    healthcheck:
      test: ["CMD", "wget", "--spider", "-q", "http://127.0.0.1:8080/healthz"]
      interval: 10s
      timeout: 3s
      retries: 3
```

Do not add `depends_on: telos-core`; the static client has no direct API dependency.

- [ ] **Step 4: Split Traefik routing**

Rename the catch-all router to `telos-web`, leave its rule/entry point/TLS behavior intact, and set `service: telos-client`. Add the corresponding load balancer. Add an explicit lower-priority `/api/v1` fallback router to core if the current specific API routers do not cover every API path; it must outrank `telos-web` and keep the bounded JSON middleware. Verify WebSocket paths remain on core.

- [ ] **Step 5: Run Compose and routing checks**

```bash
podman compose --env-file .env.example -f docker-compose.yml config --quiet
bash scripts/validate-compose.sh --env-file .env.example
bash tests/operations/runtime_hardening_test.sh
```

Expected: all exit `0`; rendered client networks equal `[telos-ingress]` and only Traefik publishes ports.

- [ ] **Step 6: Commit the route split**

```bash
git add docker-compose.yml config/dynamic/routes.yaml scripts/validate-compose.sh \
  tests/operations/runtime_hardening_test.sh tests/fixtures/compose.env
git commit -m "deploy: route standalone client and gateway separately"
```

---

### Task 3: Generate and validate node signing files safely

**Files:**
- Modify: `.env.example`
- Modify: `.gitignore`
- Modify: `cli/commands/init.sh`
- Modify: `cli/commands/doctor.sh`
- Modify: `cli/commands/start.sh`
- Create: `cli/lib/config.sh`
- Modify: `docker-compose.yml`
- Create: `tests/operations/node_config_test.sh`

**Interfaces:**
- Produces: host `TELOS_NODE_PATH`/`TELOS_CONFIG_PATH`, `node_env_path()` returning `$TELOS_NODE_PATH/node.env`, container paths `/run/telos/cursor-keys.json` and `/run/telos/hls-key`.
- Produces: `validate_node_config FILE MODE` (`MODE=production|development`) for init/start/doctor; returns zero only when all checks for that mode pass and emits redacted diagnostics to stderr.
- Consumes: existing cursor/HLS loaders in the Go gateway.

- [ ] **Step 1: Add hermetic CLI configuration tests**

Create `tests/operations/node_config_test.sh` using a copied repository skeleton under `mktemp -d`, a stub `openssl` that emits deterministic distinct bytes per invocation, and stub runtime/compose executables. Assert:

- first `telos init --dev --storage "$tmp/storage"` creates `node.env` mode `0600`, config directory mode `0700`, two files mode `0600`, valid cursor JSON, and different decoded key bytes; use production-mode fixtures for placeholder/library refusal and development-mode fixtures to permit incomplete upstream setup;
- `node.env` has a host `TELOS_CONFIG_PATH` but the two gateway path values are exactly `/run/telos/cursor-keys.json` and `/run/telos/hls-key`;
- a second init exits nonzero without changing any digest;
- `--force` is an unknown option and does not create a backup or change a file;
- interruption before the final `node.env` rename leaves no final `node.env`; the same invocation cleans only its own staging paths. A subsequent invocation refuses an orphan final config directory and prints a targeted recovery instruction; it never recursively deletes it based on a marker alone;
- start refuses missing file, wrong mode, identical keys, placeholder integration value, empty library list, and unreadable config directory before calling Compose;
- doctor names one repair action per defect and never prints `telos init --force`;
- doctor `--fix` may tighten mode but never regenerate key contents.

- [ ] **Step 2: Run the test and confirm destructive guidance/current missing files fail**

```bash
bash tests/operations/node_config_test.sh
```

Expected: nonzero because init accepts `--force`, signing files are absent, and doctor recommends regeneration.

- [ ] **Step 3: Add a non-sourcing `node.env` parser and validator**

Implement `cli/lib/config.sh` without `source .env`. Accept blank/comment lines and assignments with uppercase keys; reject duplicate keys, malformed records, NUL, multiline values, substitutions, and unsupported quoting. Use one explicit Compose-compatible encoding: generated values are single-quoted, literal apostrophes are escaped as `\\'`, and unquoted existing simple values remain readable. Never evaluate `$`, backticks, or shell syntax. Add round-trip fixture tests against rendered Compose for `$`, `#`, spaces, apostrophes, double quotes, and backslashes; malformed values fail before any write. Implement the parser/writer in a small Python standard-library helper called by Bash, with secret values passed by inherited descriptor. Expose:

```bash
node_env_path # print the resolved absolute node.env path
env_value FILE KEY
validate_signing_files FILE
validate_integrations FILE
validate_node_config FILE MODE
```

`validate_signing_files` resolves the host directory from `TELOS_CONFIG_PATH`, rejects symlinks, requires directory `0700` or stricter and files `0600` or stricter, decodes both base64 keys, requires at least 32 bytes, validates cursor JSON shape, and requires distinct bytes. Also require the active cursor key to exist, validate every key with the same minimum length, and reject any cursor key equal to the HLS root. Validate actual readability as the runtime UID from the shared runtime contract; host readability alone is insufficient. Directory owner-read/search and file owner-read must be present. Error messages name a targeted repair or restore operation; missing keys on an existing node require recovery, never automatic key regeneration.

- [ ] **Step 4: Make initialization one-way and atomic**

Remove `--force`, backup behavior, and all regeneration advice. Before writing anything, fail if `node.env`, the target config directory, or expected signing files exist. Default production `TELOS_NODE_PATH` to `/var/lib/telos` and `TELOS_CONFIG_PATH` to `$TELOS_NODE_PATH/config`; development uses `$REPO_ROOT/.telos` as the node root, add `/.telos/` to `.gitignore`, and build the config beneath a sibling mode-`0700` staging directory carrying a random invocation marker. Require root for production init and preflight/create the storage tree and persistent `state/` before committing node configuration. In production assign config owner 10001:10001 and `node.env` owner root:root. In rootless development map the runtime UID explicitly and test it separately; development behavior cannot establish certification. Existing checkout `.env` or prior config causes a refusal with an explicit import/migration instruction: validate and copy the existing values atomically into node.env, preserving all secrets, rather than running init to regenerate them. Do not move/delete the source without operator-directed migration. Rename the config directory into place, fsync its parent, then atomically rename `node.env` last. If a crash leaves the final config directory but no `node.env`, subsequent init refuses it and prints the exact orphan path plus instructions to inspect/move it before retry. A random marker is not proof that another invocation owns data. Serialize init with an exclusive lock outside the final config directory, and test concurrent invocations so neither replaces the other’s result. Write:

```json
{"active":"k1","keys":{"k1":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}}
```

as the shape of `cursor-keys.json` only; the illustrated bytes are a test fixture and MUST NOT be installed. Generate at least 32 random bytes per key using separate `openssl rand -base64 32` calls and write a separately generated value to `hls-key`. Use the runtime ownership/mapping in the shared contracts; do not relax file modes to compensate for UID mismatch. Use sibling staging files, `chmod 0600`, `fsync` through a small Python standard-library helper, and atomic rename. On any failure, remove only staging paths created by the invocation.

Set these `node.env` values:

```dotenv
TELOS_NODE_PATH=/var/lib/telos
TELOS_CONFIG_PATH=/var/lib/telos/config
TELOS_CURSOR_KEYS_FILE=/run/telos/cursor-keys.json
TELOS_HLS_SIGNING_KEY_FILE=/run/telos/hls-key
```

- [ ] **Step 5: Mount the signing directory read-only**

Add to `telos-core`:

```yaml
    volumes:
      - ${TELOS_CONFIG_PATH:?Run telos init first}:/run/telos:ro,z
```

Keep the storage mount. Set gateway `user: "10001:10001"` in production now and coordinate its writable storage ownership with Phase 4. Test file readability from that actual container identity and denial from the client container. The read-only signing mount requires no world/group readability. Do not mount the secret directory into Traefik, client, databases, Jellyfin, Grimmory, ClamAV, or the egress proxy.

- [ ] **Step 6: Make start and doctor enforce granular safety**

Source `cli/lib/config.sh` from the dispatcher/common helpers so every command shares `node_env_path`; update `start.sh`, `doctor.sh`, backup/restore/upgrade wrappers and any remaining direct `node.env` consumers. Preserve explicit env-file overrides only where already supported; never source the file. `start` calls `validate_node_config` before Compose. `doctor` reports `node.env`, signing directory, each signing file, integration completeness, storage, and Compose mount separately. Safe `--fix` may create missing empty storage directories and tighten permissions; it may not generate credentials or edit `node.env`.

- [ ] **Step 7: Run focused tests and Compose validation**

```bash
bash tests/operations/node_config_test.sh
bash -n cli/lib/config.sh cli/commands/init.sh cli/commands/doctor.sh cli/commands/start.sh
podman compose --env-file .env.example -f docker-compose.yml config --quiet
bash scripts/validate-compose.sh --env-file tests/fixtures/compose.env
```

Expected: all exit `0`; tests create fixture key files in a private temporary directory and set their modes there. Git cannot preserve mode 0600, so do not rely on committed fixture file modes. Production refuses incomplete integrations; development requires valid signing files but allows blank upstream credentials/library lists.

- [ ] **Step 8: Commit safe node initialization**

```bash
git add .env.example .gitignore cli/lib/config.sh cli/commands/init.sh cli/commands/doctor.sh \
  cli/commands/start.sh docker-compose.yml tests/operations/node_config_test.sh cli/lib/node_config.py \
  tests/fixtures/compose.env
git commit -m "cli: initialize node signing files without overwrite"
```

---

### Task 4: Add container-network integration discovery

**Files:**
- Create: `backend/integration_config.go`
- Create: `backend/integration_config_test.go`
- Modify: `backend/main.go`

**Interfaces:**
- Produces: `telos-core validate-integrations`, reading exactly one JSON object from stdin; success writes exactly one non-secret JSON response to stdout and exits 0. Failure writes nothing to stdout, emits a bounded fixed error code/message to stderr, and exits 1; usage errors exit 2.
- Produces Go types: `IntegrationValidationRequest`, `IntegrationLibrary`, `IntegrationValidationResponse`.
- Consumes: `JELLYFIN_URL` default `http://jellyfin:8096`, `GRIMMORY_URL` default `http://grimmory:6060`.

- [ ] **Step 1: Write HTTP contract tests before the command mode**

In `backend/integration_config_test.go`, use two `httptest.Server` instances and inject their URLs. Cover:

- Jellyfin receives `GET /Library/VirtualFolders` and exact `X-Emby-Token`;
- Jellyfin response maps stable `ItemId`/`Name`, removes blank IDs, and sorts by name then ID;
- Grimmory receives credentials only in `POST /api/v1/auth/login`, then bearer auth in `GET /api/v1/libraries`;
- Grimmory response maps only stable ID/name and sorts deterministically;
- non-2xx, invalid JSON, redirect to another host, oversize response, empty library set, timeout, and duplicate ID fail distinctly;
- serialized success and error output never contain either supplied secret;
- stdin trailing JSON or unknown request keys fail.

Define the following production types in `integration_config.go`; tests import/use them from the same package and must not redeclare them:

```go
type IntegrationValidationRequest struct {
    JellyfinToken   string `json:"jellyfinToken"`
    GrimmoryUser    string `json:"grimmoryUser"`
    GrimmoryPassword string `json:"grimmoryPassword"`
}

type IntegrationLibrary struct {
    ID   string `json:"id"`
    Name string `json:"name"`
}

type IntegrationValidationResponse struct {
    Jellyfin []IntegrationLibrary `json:"jellyfin"`
    Grimmory []IntegrationLibrary `json:"grimmory"`
}
```

- [ ] **Step 2: Run the focused Go test and observe missing symbols**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 \
  go test ./... -run 'TestValidateIntegrations|TestRunIntegrationValidationCommand'
```

Expected: compile failure for missing types/functions.

- [ ] **Step 3: Implement bounded upstream discovery**

In `integration_config.go`, create a dedicated client with a 15-second total timeout, maximum 1 MiB JSON bodies, no cross-host redirects, and explicit status checks. Validate nonblank inputs without echoing them. For Jellyfin call `GET /Library/VirtualFolders` with `X-Emby-Token`. For Grimmory call the existing login endpoint, extract the access token, then call `GET /api/v1/libraries`. Return only library IDs and names.

Do not log request bodies, upstream authorization headers, or raw upstream error bodies. Expose `validateIntegrationConfig(ctx context.Context, req IntegrationValidationRequest, jellyfinURL, grimmoryURL string) (IntegrationValidationResponse, error)` and `runIntegrationValidationCommand(in io.Reader, out, errOut io.Writer) error`. Read at most 64 KiB from stdin with `DisallowUnknownFields`, require EOF after one object, and reject all redirects (including same-host protocol downgrades). Decode concrete provider response structs, not guessed generic maps. Jellyfin’s `ItemId`/`Name` shape is specified above; confirm Grimmory’s library endpoint and response shape against the actual pinned service before freezing the adapter. Store a sanitized response fixture plus upstream version/API reference; an `httptest` invented shape alone cannot establish compatibility. If that upstream is unavailable, finish parser/unit work and mark the provider validation checkpoint unavailable, without claiming live discovery works.

- [ ] **Step 4: Dispatch before production/database startup**

In `backend/main.go`, add:

```go
if mode == "validate-integrations" {
    if err := runIntegrationValidationCommand(os.Stdin, os.Stdout, os.Stderr); err != nil {
        fmt.Fprintln(os.Stderr, "integration validation failed:", redactIntegrationError(err))
        os.Exit(1)
    }
    return
}
```

This branch must occur before `validateSecrets`, cursor/HLS loading, database, or Redis initialization. Reject arguments after the mode name.

- [ ] **Step 5: Run backend checks**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 gofmt -w integration_config.go integration_config_test.go main.go
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go vet ./...
```

Expected: all exit `0`; integration tests self-contain their HTTP upstreams.

- [ ] **Step 6: Commit integration discovery**

```bash
git add backend/integration_config.go backend/integration_config_test.go backend/main.go \
  backend/jellyfin_catalog.go backend/grimmory_catalog.go
git commit -m "backend: validate upstream libraries from container networks"
```

---

### Task 5: Implement guided Jellyfin and Grimmory setup

**Files:**
- Create: `docker-compose.setup.yml`
- Create: `cli/commands/configure.sh`
- Modify: `cli/lib/common.sh`
- Modify: `cli/lib/config.sh`
- Modify: `scripts/provision-jellyfin.sh`
- Modify: `scripts/provision-grimmory.sh`
- Create: `tests/operations/configure_integrations_test.sh`
- Create: `documentation/operations/guided-integrations.md`
- Modify: `documentation/operations/install.md`
- Modify: `frontend/AGENTS.md`

**Interfaces:**
- Produces: `telos configure integrations` and optional `--setup` lifecycle.
- Consumes: `validate-integrations` JSON stdin/stdout contract from Task 4.
- Produces: atomically updated `JELLYFIN_ADMIN_TOKEN`, `JELLYFIN_LIBRARY_IDS`, `GRIMMORY_ADMIN_USER`, `GRIMMORY_LIBRARY_IDS`, and optionally `GRIMMORY_ADMIN_PASSWORD` only when explicitly replaced. All other keys/comments remain byte-identical; the existing password is reused for a blank prompt.

- [ ] **Step 1: Add overlay and interactive-flow tests**

Create `tests/operations/configure_integrations_test.sh` with stub Compose/runtime commands and a fake validation command. Test:

- rendered overlay publishes only `127.0.0.1:${JELLYFIN_SETUP_PORT:-8096}:8096` and `127.0.0.1:${GRIMMORY_SETUP_PORT:-6060}:6060`;
- `--setup` starts only `jellyfin`, `grimmory-db`, `grimmory`, and required egress dependencies with base plus setup files;
- prompt input is not echoed when a PTY is available and never appears in captured output;
- the validation request reaches the fake process over stdin, not argv;
- library choices display names plus stable IDs, accept comma-separated numeric selections, deduplicate while preserving displayed order, and reject an empty/unknown selection;
- a validation error leaves `node.env` byte-identical;
- interruption during atomic rewrite leaves `node.env` byte-identical and deletes staging files;
- success changes the four required integration keys plus the password only when explicitly replaced; it preserves mode `0600` and uses the shared parser/writer from Task 3;
- setup teardown removes bindings before returning success and on validation failure, SIGINT, and SIGTERM. A cleanup failure makes the command fail and names the exact removal command; tests must not pass after leaving a setup port open.

- [ ] **Step 2: Run the test and observe missing overlay/command**

```bash
bash tests/operations/configure_integrations_test.sh
```

Expected: nonzero because `telos configure` and the setup overlay do not exist.

- [ ] **Step 3: Create the loopback-only overlay**

Add only these port overrides:

```yaml
services:
  jellyfin:
    ports:
      - "127.0.0.1:${JELLYFIN_SETUP_PORT:-8096}:8096"
  grimmory:
    ports:
      - "127.0.0.1:${GRIMMORY_SETUP_PORT:-6060}:6060"
```

The production Compose file remains port-free for both services. Add a Compose validation check that fails any non-loopback setup binding.

- [ ] **Step 4: Add an argv-safe Compose helper for explicit overlays**

In `cli/lib/common.sh`, replace string-valued driver execution with an array-returning helper or explicit branches so paths/arguments remain separate. Add:

```text
compose_with FILE_1 [FILE_2] -- ARG_1 [ARG_2]
```

The `FILE_*` operands are overlays only; the helper includes the base exactly once, validates repository-local overlay paths, adds explicit `--env-file "$(node_env_path)"` and project directory, and never invokes `eval`. Use the shared Podman/Compose driver contract, not whichever unrelated Docker provider happens to be installed. Add a test invoking the installed CLI from a directory outside the checkout. No-overlay operation is `compose_with -- ARG...`.

- [ ] **Step 5: Implement `telos configure integrations`**

The command flow is:

1. require an existing `node.env` and signing-file validity; acquire an exclusive config lock shared with init/start; refuse setup on an already running production stack so cleanup cannot stop somebody else’s service;
2. build the headless gateway validation image (`compose_with -- build telos-core`) before first use; with `--setup`, start only the named loopback setup services/dependencies and print local URLs plus an exact SSH tunnel example using the configured ports. Without `--setup`, require the upstream containers/network already running and do not manage their lifecycle;
3. wait for the operator to confirm both first-run UIs are complete;
4. read Jellyfin token and optional Grimmory account/password using `read -r -s`, restoring echo via trap;
5. encode one JSON object with Python reading NUL-delimited secret values from a dedicated inherited file descriptor—not argv or a newly populated process environment—and pipe it to:

```bash
compose_with docker-compose.setup.yml -- run --rm -T --no-deps telos-core ./telos-core validate-integrations
```

Use the overlay in that command only when `--setup` is present; otherwise use `compose_with -- run ...`. `backend/Dockerfile` defines CMD, not ENTRYPOINT, so `./telos-core` is required. The stdin request is the sole source of supplied discovery credentials.

6. parse the non-secret response; display indexed ID/name choices;
7. require at least one valid ID for each integration;
8. repeat discovery using the same credentials, then verify every selected stable ID still exists in the second result; the request schema has no selection fields, so do not invent a second incompatible request shape;
9. atomically update `node.env` without sourcing it;
10. when `--setup` was used, stop/remove only the setup containers this invocation created (never `down -v`) and verify neither setup host port listens. Register cleanup traps before starting them; normal production start recreates them from the base file without ports.

Support `-h|--help` and `--setup`; reject every other flag. Do not offer token/password flags.

- [ ] **Step 6: Convert legacy provisioning scripts to guidance**

Make both scripts print `telos configure integrations --setup` and exit `3`; they must not claim or attempt automated third-party account creation.

- [ ] **Step 7: Write the guided runbook**

Document exact local and SSH-tunneled URLs, Jellyfin API-key creation, Grimmory gateway account setup, library creation, stable-ID selection, expected validation output, stopping the overlay, rerunning validation, credential rotation, and safe failure recovery. State clearly that setup ports are loopback-only and must be closed before normal use. Update `frontend/AGENTS.md` so browser calls remain same-origin through `telos-client`/Traefik while Tauri uses the existing explicit remote base URL and token mode; remove the obsolete statement that production assets are served directly by the Go gateway.

- [ ] **Step 8: Run focused tests**

```bash
bash tests/operations/configure_integrations_test.sh
bash scripts/validate-compose.sh --env-file tests/fixtures/compose.env
bash -n cli/commands/configure.sh cli/lib/common.sh cli/lib/config.sh
./telos configure --help
```

Expected: all exit `0`; help contains no secret-valued flags.

- [ ] **Step 9: Commit guided setup**

```bash
git add docker-compose.setup.yml cli/commands/configure.sh cli/lib/common.sh cli/lib/config.sh \
  scripts/provision-jellyfin.sh scripts/provision-grimmory.sh \
  tests/operations/configure_integrations_test.sh \
  documentation/operations/guided-integrations.md documentation/operations/install.md \
  frontend/AGENTS.md scripts/validate-compose.sh
git commit -m "cli: guide and validate media integrations"
```

---

### Task 6: Prove the hosted browser service and degraded-module behavior

**Files:**
- Create: `scripts/smoke-browser-node.sh`
- Create: `tests/operations/browser_node_smoke_test.sh`
- Modify: `frontend/e2e/bootstrap.spec.ts`, `frontend/e2e/chat.spec.ts`, `frontend/e2e/stream.spec.ts`, `frontend/e2e/library.spec.ts`, `frontend/e2e/files.spec.ts`, `frontend/e2e/failure-recovery.spec.ts` — hosted four-module and degraded-upstream journeys.

**Interfaces:**
- Produces: a cookie-mode, same-origin browser smoke command.
- Consumes: public HTTPS origin, a private stdin JSON credential bundle (owner username/password, invited member username/password, bootstrap token only with the explicit empty-node flag), and fixture manifest naming one video, one audio item, one readable ebook, and one writable Chat channel. The wrapper never prints the bundle. External outage control belongs to the reference-host harness below.

- [ ] **Step 1: Write fixture tests for the smoke command**

Use an ephemeral local HTTPS fixture with a generated test CA installed only into the test client's CA file. Assert the script:

- rejects HTTP and certificate verification failure;
- verifies root HTML comes from client and `/api/v1/health`/`ready` come from core;
- bootstraps an owner only when explicitly told the node is empty;
- logs in, creates an invite, accepts it as a member, and uses a cookie jar with mode `0600`;
- creates/reads a Chat message, lists Stream/Library, fetches and decodes a playable HLS fixture, reads a licensed test ebook through the reader, uploads/downloads/deletes a Files fixture, and creates/reads commentary. Missing media/ebook fixtures fail the journey; list-only success is insufficient;
- verifies a Jellyfin outage degrades Stream without breaking Chat/Files authentication;
- verifies a Grimmory outage degrades Library without breaking Chat/Files authentication;
- deletes all temporary credentials/cookies on every exit;
- writes an observed-result sidecar only after all assertions finish; the shared runner alone emits the evidence envelope. Include recordKind=rehearsal|candidate, source commit, status and candidateLockDigest (null only for source-only rehearsal; otherwise the verified rehearsal/final lock digest); place journey checks in a declared report attachment; use the exact common schema rather than defining another envelope.

Do not put bootstrap, owner, or member credentials in argv. Accept them through inherited file descriptors or interactive prompts.

- [ ] **Step 2: Run fixture tests and observe the missing command**

```bash
bash tests/operations/browser_node_smoke_test.sh
```

Expected: nonzero because the smoke command does not exist.

- [ ] **Step 3: Implement the bounded HTTPS smoke command**

Expose:

```text
scripts/smoke-browser-node.sh \
  --public-url https://community.example.net \
  (--rehearsal --source-commit SHA [--rehearsal-lock PATH] | --candidate-lock PATH) \
  --result-out PATH --fixture-manifest PATH \
  [--bootstrap-empty-node]
```

Require `https`, refuse IP/domain mismatch, use `curl --fail-with-body --max-time`, preserve cookies only in a `0700` temporary directory, validate content types before parsing, and redact response bodies on auth failure. In candidate mode resolve identity from the lock, never a caller-provided digest. Rehearsal mode records source identity through the shared rehearsal schema and cannot emit final candidate evidence. Keep the common runner/evidence interface rather than independently implementing envelope hashing. The script drives real browser journeys through a dedicated `frontend/e2e/hosted-node.spec.ts` (create) with no route mocking. A local fixture mode tests protocol/error handling only and is explicitly excluded from release evidence.

- [ ] **Step 4: Extend Playwright member journeys**

Against an already running local stack, cover navigation/rendering for Chat, Stream, Library, Files, commentary, Settings, and Admin. Add module-local outage expectations. Create `scripts/rehearse-browser-node.sh` (reference-host controller) to start the smoke process, stop only Jellyfin then Grimmory in separate stages, assert Chat/Files stay usable, and restore each service in a trap. The HTTPS client cannot itself cause a remote service outage; use a private stage-control pipe between controller and browser worker. Never pass arbitrary shell commands through the smoke CLI. Reuse current page objects/fixtures; do not add a mocked E2E pass to candidate evidence. Explicitly pass `E2E_BASE_URL` for hosted testing and require selected live tests to execute with zero skips; existing specs select `test.skip` when credentials are missing (`frontend/e2e/stream.spec.ts:5-11`).

- [ ] **Step 5: Implement only demonstrated degraded-state gaps**

If the tests show that a module outage destroys the shell or produces an unclassified generic error, map existing upstream `502/503` responses to the affected module's unavailable state. Do not fabricate catalog entries or change global readiness semantics to hide an upstream failure.

- [ ] **Step 6: Run browser tests**

```bash
bash tests/operations/browser_node_smoke_test.sh
cd frontend
npm run lint
npx tsc --noEmit
npm run test:unit
npm run build
npx playwright test
cd ..
```

Expected: all exit `0`; E2E requires a real local stack as documented and may not be recorded as candidate evidence from fixtures.

- [ ] **Step 7: Commit browser journey proof**

```bash
git add scripts/smoke-browser-node.sh scripts/rehearse-browser-node.sh tests/operations/browser_node_smoke_test.sh \
  frontend/e2e frontend/src/lib/api.ts frontend/src/app backend/health_test.go \
  backend/*_test.go
git commit -m "test: prove hosted browser member journeys"
```

---

### Task 7: Execute the clean-reference-host gate and wire Phase 2 CI

**Files:**
- Modify: `tests/operations/install_test.sh`
- Modify: `install.sh`, `cli/lib/preflight.sh`, `cli/lib/common.sh`
- Modify: `documentation/operations/install.md`
- Modify: `ci/phase-gates.json`
- Modify: `.github/workflows/ci.yml`
- Create: `documentation/operations/clean-install-evidence.md`

**Interfaces:**
- Produces local gates: `static-client-image`, `standalone-compose`, `node-config`, `integration-discovery`, `guided-integration-fixtures`, `browser-node-fixtures`.
- Produces external gate: `clean-install`.

- [ ] **Step 1: Replace the install existence check**

Make `tests/operations/install_test.sh` exercise install/preflight logic against fixture `/etc/os-release`, architecture, Podman version, storage, ports, DNS, and filesystem capabilities. Assert Ubuntu `ID=ubuntu`, numeric `VERSION_ID>=24.04`, x86-64, and Podman>=5 are necessary for supported-server status. Reject Fedora, Debian, other derivatives, Windows/WSL, macOS, Ubuntu<24.04, ARM, Docker-only, and Podman<5 as unsupported, with no best-effort path. A newer Ubuntu release passes the support policy but is not labeled verified until its live checks run. Add injectable os-release/tool probes to `cli/lib/preflight.sh`; update `install.sh` and doctor to call the same implementation. Missing trusted HTTPS/DNS leaves the external check `not_run`, not the local parser fixture suite.

- [ ] **Step 2: Run install fixtures and confirm the current test is insufficient**

```bash
bash tests/operations/install_test.sh
```

Expected: nonzero until behavioral fixtures are implemented.

- [ ] **Step 3: Add local Phase 2 gates to the catalog and CI**

Add explicit argv entries for:

```text
static-client-image
standalone-compose
node-config
integration-discovery
guided-integration-fixtures
browser-node-fixtures
frontend-quality
```

Make `clean-install` external and dependent on them. CI runs the local entries from a clean checkout; it cannot mark `clean-install` passed.

- [ ] **Step 4: Document the live clean-host procedure**

The runbook must start from a newly provisioned Ubuntu 24.04 x86-64 host with Podman 5+, a real DNS name, and working inbound 80/443. Before the live attempt, finish a bounded prerequisite validation deliverable: record exact package/source URLs, versions, signing keys/checksums, and installation commands for Podman>=5 and the selected Compose provider, then verify them in a disposable 24.04 VM. Ubuntu's normal `apt install podman` must not be assumed to satisfy the version floor ([official Ubuntu package inventory](https://packages.ubuntu.com/search?keywords=podman), [Podman installation instructions](https://podman.io/docs/installation)). If no verified source is available, mark this prerequisite unavailable and continue local implementation; do not fabricate an installation recipe or lower the floor. Use the shared runtime identity and key ownership contract. Remove init's silent production fallback to 8081/8443: fail preflight with a targeted public-port remedy instead; dev mode may retain alternate ports. Test public 80/443 reachability from outside the node, as HTTP-01 cannot be proven with loopback curl alone. It records host identity, tool versions, source/artifact identity, install start/end, initialization file modes, loopback setup bindings, selected library IDs, normal-start port inventory, public certificate chain, owner/invite flow, and `smoke-browser-node.sh` result. Any preinstalled Telos volume or reused evidence directory invalidates the run.

- [ ] **Step 5: Run the Phase 2 local exit suite**

```bash
bash tests/operations/static_client_test.sh
bash tests/operations/runtime_hardening_test.sh
bash tests/operations/node_config_test.sh
bash tests/operations/configure_integrations_test.sh
bash tests/operations/browser_node_smoke_test.sh
bash tests/operations/install_test.sh
bash scripts/validate-compose.sh --env-file tests/fixtures/compose.env
bash scripts/check-product-truth.sh
bash scripts/verify-clean-checkout.sh --inventory-only
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
cd frontend && npm run lint && npx tsc --noEmit && npm run test:unit && npm run build && cd ..
```

Expected: every local command exits `0`.

- [ ] **Step 6: Run the external clean-host checkpoint**

On the prepared reference host, follow `documentation/operations/clean-install-evidence.md` and run the HTTPS smoke command in source-bound rehearsal mode. Expected: all journey assertions pass in the rehearsal record. If DNS, trusted TLS, host isolation, or upstream setup is unavailable, record the missing prerequisite and `not_run`; complete/review local deliverables and allow independent Phase 3/4 work. Final `clean-install` candidate evidence is rerun in Phase 5 against the frozen artifacts; no Phase 2 source lock or early rehearsal can satisfy it.

- [ ] **Step 7: Commit CI/runbook integration**

```bash
git add tests/operations/install_test.sh install.sh cli/lib/preflight.sh cli/lib/common.sh documentation/operations/install.md \
  documentation/operations/clean-install-evidence.md ci/phase-gates.json \
  .github/workflows/ci.yml
git commit -m "ci: gate clean standalone browser deployments"
```

- [ ] **Step 8: Record the phase review checkpoint**

Review the live evidence and merged API/routing/configuration contracts. Request code review before Phase 3 and Phase 4 branch from this baseline.
