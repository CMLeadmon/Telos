# Standalone Beta Phase 2: Clean Linux Deployment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a fresh Ubuntu 24.04 LTS x86-64 host install, configure, and serve all four Telos modules in a browser through the authoritative Bash CLI and platform-trusted HTTPS.

**Architecture:** Build the static Next.js export into a separate unprivileged `telos-client` service connected only to the ingress network. Keep every API route on the headless Go gateway and route the Traefik catch-all to the client. Generate signing files during one-time initialization, mount them read-only, and finish third-party setup through a loopback-only Compose overlay plus a container-network validation command. Refuse incomplete or destructive starts.

**Tech Stack:** Bash `telos` CLI, Podman 5+, Compose, Go 1.26.5, Next.js 16.2.10 static export, nginx unprivileged Alpine runtime, Traefik, Jellyfin HTTP API, Grimmory HTTP API, Playwright 1.61.1.

**Spec:** `docs/superpowers/specs/2026-08-20-standalone-beta-readiness-design.md`

## Global Constraints

- Execute after Phase 1 is merged. Any external check without a real host remains `not_run` and blocks final certification.
- Production architecture is the headless `telos-core` service plus `telos-client`; do not enable `EMBED_FRONTEND` in production Compose.
- `telos-client` has only `telos-ingress`, no host ports, no secrets, no volumes containing member data, and no access to `telos-backend` or `telos-db`.
- Traefik is the only public listener. Browser API/WebSocket calls remain same-origin.
- `telos init` never overwrites `.env`, signing files, storage, or durable volumes. Remove `--force` and every instruction recommending it.
- Production start requires non-placeholder upstream credentials, at least one stable Jellyfin library ID, at least one stable Grimmory library ID, and readable, mode-correct signing files.
- Guided setup exposes Jellyfin and Grimmory only on `127.0.0.1`. A remote operator uses an SSH tunnel; no setup port may bind `0.0.0.0` or a public interface.
- Secret prompts disable terminal echo. Secrets travel over a dedicated inherited file descriptor or Compose environment interpolation, never command-line arguments, a process environment created only for JSON encoding, log output, evidence, or temporary world-readable files.
- The backend validation mode talks to upstreams only over their HTTP APIs. Preserve the copyleft process boundary.
- Do not edit any applied migration; this phase requires no schema migration.
- Keep Phase 1's evidence/gate interfaces stable.

---

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
- Modify: `.env.example` — host config path and supported Tauri origins input.
- Modify: `.gitignore` — ignore generated node configuration.
- Modify: `cli/commands/init.sh` — safe key generation and no overwrite path.
- Modify: `cli/commands/doctor.sh` — granular diagnosis and non-destructive repairs.
- Modify: `cli/commands/start.sh` — preflight completeness refusal.
- Create: `cli/lib/config.sh` — parse/validate `.env` and signing-file inputs without sourcing it.
- Create: `tests/operations/node_config_test.sh` — init/doctor/start behavior tests.
- Create: `backend/integration_config.go` — upstream discovery and command-mode JSON contract.
- Create: `backend/integration_config_test.go` — `httptest` discovery tests and secret-redaction assertions.
- Modify: `backend/main.go` — dispatch `validate-integrations` before normal startup.
- Create: `docker-compose.setup.yml` — loopback-only first-run overlay.
- Create: `cli/commands/configure.sh` — `telos configure integrations` guided workflow.
- Create: `tests/operations/configure_integrations_test.sh` — prompt, selection, atomic write, and failure tests.
- Create: `scripts/smoke-browser-node.sh` — external HTTPS and member-journey probe.
- Create: `tests/operations/browser_node_smoke_test.sh` — deterministic fixture server tests.
- Modify: `tests/operations/install_test.sh` — clean reference-host contract.
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

Use a bounded readiness loop instead of a sleep. Also inspect the image history and fail if `.env`, `telos-secrets.json`, source maps, `node_modules`, or the build-stage filesystem appears in the runtime image.

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

Phase 4 replaces tags with reviewed digests. Do not add runtime Node, a package manager, source, or shell-written configuration.

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

Extend `scripts/validate-compose.sh` with `standalone-client` and `route-split` checks. Parse rendered Compose and `config/dynamic/routes.yaml` and assert:

- `telos-client` exists, builds `frontend/Dockerfile`, has only `telos-ingress`, publishes no ports, mounts no volumes, receives no environment variable containing `PASSWORD`, `TOKEN`, `SECRET`, `DATABASE`, `REDIS`, `STORAGE`, `JELLYFIN`, or `GRIMMORY`, and has a `/healthz` health check;
- `telos-core` still uses the default headless target and joins all three required networks;
- every router whose rule can match `/api/v1/` targets `telos-core`;
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
- Produces: host `TELOS_CONFIG_PATH`, container paths `/run/telos/cursor-keys.json` and `/run/telos/hls-key`.
- Produces: `validate_node_config production|development` for init/start/doctor.
- Consumes: existing cursor/HLS loaders in the Go gateway.

- [ ] **Step 1: Add hermetic CLI configuration tests**

Create `tests/operations/node_config_test.sh` using a copied repository skeleton under `mktemp -d`, a stub `openssl` that emits deterministic distinct bytes per invocation, and stub runtime/compose executables. Assert:

- first `telos init --dev --storage "$tmp/storage"` creates `.env` mode `0600`, config directory mode `0700`, two files mode `0600`, valid cursor JSON, and different decoded key bytes;
- `.env` has a host `TELOS_CONFIG_PATH` but the two gateway path values are exactly `/run/telos/cursor-keys.json` and `/run/telos/hls-key`;
- a second init exits nonzero without changing any digest;
- `--force` is an unknown option and does not create a backup or change a file;
- interruption before the final `.env` rename leaves no final `.env`; a subsequent init recognizes and removes only its marker-owned orphan config staging/final directory before generating fresh keys;
- start refuses missing file, wrong mode, identical keys, placeholder integration value, empty library list, and unreadable config directory before calling Compose;
- doctor names one repair action per defect and never prints `telos init --force`;
- doctor `--fix` may tighten mode but never regenerate key contents.

- [ ] **Step 2: Run the test and confirm destructive guidance/current missing files fail**

```bash
bash tests/operations/node_config_test.sh
```

Expected: nonzero because init accepts `--force`, signing files are absent, and doctor recommends regeneration.

- [ ] **Step 3: Add a non-sourcing `.env` parser and validator**

Implement `cli/lib/config.sh` without `source .env`. Parse only `^[A-Z][A-Z0-9_]*=.*$`, reject duplicate required keys and NUL/newline values, and expose:

```bash
env_value FILE KEY
validate_signing_files FILE
validate_integrations FILE
validate_node_config FILE MODE
```

`validate_signing_files` resolves the host directory from `TELOS_CONFIG_PATH`, rejects symlinks, requires directory `0700` or stricter and files `0600` or stricter, decodes both base64 keys, requires at least 32 bytes, validates cursor JSON shape, and requires distinct bytes. Error messages name a repair command that affects only the missing/bad file.

- [ ] **Step 4: Make initialization one-way and atomic**

Remove `--force`, backup behavior, and all regeneration advice. Before writing anything, fail if `.env`, the target config directory, or expected signing files exist. Default `TELOS_CONFIG_PATH` to `$REPO_ROOT/.telos/config`, add `/.telos/` to `.gitignore`, and build the config beneath a sibling mode-`0700` staging directory carrying a random invocation marker. Preflight/create the storage tree before committing node configuration. Rename the config directory into place, fsync its parent, then atomically rename `.env` last. If a crash leaves config but no `.env`, a later init may remove it only when the private marker matches the closed expected inventory; any unmarked/extra content requires operator review. Write:

```json
{"active":"k1","keys":{"k1":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}}
```

to `cursor-keys.json`; write a separately generated base64 value to `hls-key`. Use sibling staging files, `chmod 0600`, `fsync` through a small Python standard-library helper, and atomic rename. On any failure, remove only staging paths created by the invocation.

Set these `.env` values:

```dotenv
TELOS_CONFIG_PATH=/absolute/host/path/.telos/config
TELOS_CURSOR_KEYS_FILE=/run/telos/cursor-keys.json
TELOS_HLS_SIGNING_KEY_FILE=/run/telos/hls-key
```

- [ ] **Step 5: Mount the signing directory read-only**

Add to `telos-core`:

```yaml
    volumes:
      - ${TELOS_CONFIG_PATH:?Run telos init first}:/run/telos:ro,z
```

Keep the storage mount. Do not mount the secret directory into Traefik, client, databases, Jellyfin, Grimmory, ClamAV, or the egress proxy.

- [ ] **Step 6: Make start and doctor enforce granular safety**

Source `cli/lib/config.sh` from `start.sh` and `doctor.sh`. `start` calls `validate_node_config` before Compose. `doctor` reports `.env`, signing directory, each signing file, integration completeness, storage, and Compose mount separately. Safe `--fix` may create missing empty storage directories and tighten permissions; it may not generate credentials or edit `.env`.

- [ ] **Step 7: Run focused tests and Compose validation**

```bash
bash tests/operations/node_config_test.sh
bash -n cli/lib/config.sh cli/commands/init.sh cli/commands/doctor.sh cli/commands/start.sh
podman compose --env-file .env.example -f docker-compose.yml config --quiet
bash scripts/validate-compose.sh --env-file tests/fixtures/compose.env
```

Expected: all exit `0`; fixture `.env` points to fixture key files with safe modes.

- [ ] **Step 8: Commit safe node initialization**

```bash
git add .env.example .gitignore cli/lib/config.sh cli/commands/init.sh cli/commands/doctor.sh \
  cli/commands/start.sh docker-compose.yml tests/operations/node_config_test.sh \
  tests/fixtures/compose.env tests/fixtures/config
git commit -m "cli: initialize node signing files without overwrite"
```

---

### Task 4: Add container-network integration discovery

**Files:**
- Create: `backend/integration_config.go`
- Create: `backend/integration_config_test.go`
- Modify: `backend/main.go`

**Interfaces:**
- Produces: `telos-core validate-integrations`, reading exactly one JSON object from stdin and writing exactly one non-secret JSON response to stdout.
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

Define exact types in the test:

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

Do not log request bodies, upstream authorization headers, or raw upstream error bodies.

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
- Produces: atomically updated `JELLYFIN_ADMIN_TOKEN`, `JELLYFIN_LIBRARY_IDS`, `GRIMMORY_ADMIN_USER`, and `GRIMMORY_LIBRARY_IDS`; existing Grimmory password stays the initialized secret unless the operator explicitly enters a replacement.

- [ ] **Step 1: Add overlay and interactive-flow tests**

Create `tests/operations/configure_integrations_test.sh` with stub Compose/runtime commands and a fake validation command. Test:

- rendered overlay publishes only `127.0.0.1:${JELLYFIN_SETUP_PORT:-8096}:8096` and `127.0.0.1:${GRIMMORY_SETUP_PORT:-6060}:6060`;
- `--setup` starts only `jellyfin`, `grimmory-db`, `grimmory`, and required egress dependencies with base plus setup files;
- prompt input is not echoed when a PTY is available and never appears in captured output;
- the validation request reaches the fake process over stdin, not argv;
- library choices display names plus stable IDs, accept comma-separated numeric selections, deduplicate while preserving displayed order, and reject an empty/unknown selection;
- a validation error leaves `.env` byte-identical;
- interruption during atomic rewrite leaves `.env` byte-identical and deletes staging files;
- success changes only the four integration keys and preserves mode `0600`;
- setup teardown removes bindings before returning success.

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

It always includes `docker-compose.yml`, adds only validated repository-local overlay paths before `--`, and never invokes `eval`.

- [ ] **Step 5: Implement `telos configure integrations`**

The command flow is:

1. require an existing `.env` and signing-file validity;
2. with `--setup`, start the loopback setup services and print local URLs plus exact SSH tunnel example using the configured ports;
3. wait for the operator to confirm both first-run UIs are complete;
4. read Jellyfin token and optional Grimmory account/password using `read -r -s`, restoring echo via trap;
5. encode one JSON object with Python reading NUL-delimited secret values from a dedicated inherited file descriptor—not argv or a newly populated process environment—and pipe it to:

```bash
compose_with docker-compose.setup.yml -- run --rm -T --no-deps telos-core validate-integrations
```

6. parse the non-secret response; display indexed ID/name choices;
7. require at least one valid ID for each integration;
8. validate the selected tuple a second time;
9. atomically update `.env` without sourcing it;
10. stop setup services/remove the setup containers and verify neither setup host port listens.

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
- Consumes: public HTTPS origin, bootstrap token or invited test account through stdin/file descriptor, and four module routes.

- [ ] **Step 1: Write fixture tests for the smoke command**

Use an ephemeral local HTTPS fixture with a generated test CA installed only into the test client's CA file. Assert the script:

- rejects HTTP and certificate verification failure;
- verifies root HTML comes from client and `/api/v1/health`/`ready` come from core;
- bootstraps an owner only when explicitly told the node is empty;
- logs in, creates an invite, accepts it as a member, and uses a cookie jar with mode `0600`;
- creates/reads a Chat message, lists Stream, lists Library, uploads/downloads/deletes a Files fixture, and creates/reads commentary on at least one available target;
- verifies a Jellyfin outage degrades Stream without breaking Chat/Files authentication;
- verifies a Grimmory outage degrades Library without breaking Chat/Files authentication;
- deletes all temporary credentials/cookies on every exit;
- emits an evidence envelope only after all assertions finish.

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
  --candidate-lock PATH \
  --evidence-out PATH \
  [--bootstrap-empty-node]
```

Require `https`, refuse IP/domain mismatch, use `curl --fail-with-body --max-time`, preserve cookies only in a `0700` temporary directory, validate content types before parsing, and redact response bodies on auth failure. Resolve candidate identity from the lock; do not accept a caller-provided digest string.

- [ ] **Step 4: Extend Playwright member journeys**

Against an already running local stack, cover navigation/rendering for Chat, Stream, Library, Files, commentary, Settings, and Admin. Add module-local outage expectations. Reuse current page objects/fixtures; do not add a mocked E2E pass to candidate evidence.

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
git add scripts/smoke-browser-node.sh tests/operations/browser_node_smoke_test.sh \
  frontend/e2e frontend/src/lib/api.ts frontend/src/app backend/health_test.go \
  backend/*_test.go
git commit -m "test: prove hosted browser member journeys"
```

---

### Task 7: Execute the clean-reference-host gate and wire Phase 2 CI

**Files:**
- Modify: `tests/operations/install_test.sh`
- Modify: `documentation/operations/install.md`
- Modify: `ci/phase-gates.json`
- Modify: `.github/workflows/ci.yml`
- Create: `documentation/operations/clean-install-evidence.md`

**Interfaces:**
- Produces local gates: `static-client-image`, `standalone-compose`, `node-config`, `integration-discovery`, `guided-integration-fixtures`, `browser-node-fixtures`.
- Produces external gate: `clean-install`.

- [ ] **Step 1: Replace the install existence check**

Make `tests/operations/install_test.sh` exercise install/preflight logic against fixture `/etc/os-release`, architecture, Podman version, storage, ports, DNS, and filesystem capabilities. Assert only Ubuntu 24.04+x86-64+Podman 5+ can produce the supported-reference result; Fedora/Debian/ARM report best-effort without satisfying `clean-install`; missing trusted HTTPS/DNS produces `not_run`.

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

The runbook must start from a newly provisioned Ubuntu 24.04 x86-64 host with Podman 5+, a real DNS name, and working inbound 80/443. It records host identity, tool versions, source/artifact identity, install start/end, initialization file modes, loopback setup bindings, selected library IDs, normal-start port inventory, public certificate chain, owner/invite flow, and `smoke-browser-node.sh` result. Any preinstalled Telos volume or reused evidence directory invalidates the run.

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

On the prepared reference host, follow `documentation/operations/clean-install-evidence.md` and run the HTTPS smoke command with a Phase 2 source lock. Expected: `clean-install` envelope status `passed`. If DNS, trusted TLS, host isolation, or upstream setup is unavailable, record `not_run` and stop; do not begin desktop certification.

- [ ] **Step 7: Commit CI/runbook integration**

```bash
git add tests/operations/install_test.sh documentation/operations/install.md \
  documentation/operations/clean-install-evidence.md ci/phase-gates.json \
  .github/workflows/ci.yml
git commit -m "ci: gate clean standalone browser deployments"
```

- [ ] **Step 8: Record the phase review checkpoint**

Review the live evidence and merged API/routing/configuration contracts. Request code review before Phase 3 and Phase 4 branch from this baseline.
