# Headless Backend & API Contract Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Decouple backend Go gateway compilation from frontend static assets by adding build-tagged asset embedding, automated OpenAPI 3.1 contract generation, and CI verification gates.

**Architecture:** Static asset embedding is isolated behind the `embedfrontend` build tag, making headless mode the default build target for `telos-core`. An AST-driven generator (`backend/cmd/genopenapi`) parses the Go route table in `backend/main.go` to emit `backend/api/openapi.json`, which drives type-safe client generation into `frontend/src/lib/generated/`. CI jobs verify headless compilation with `frontend/` absent and enforce contract freshness via automated diff checks.

**Tech Stack:** Go 1.26.5 (`//go:build`), OpenAPI 3.1 (`openapi-typescript`), Dockerfile multi-stage build, GitHub Actions CI, TypeScript 5.9.3, Podman.

## Global Constraints

- Go toolchain is **1.26.5**; Go is **not installed on the host**. All backend toolchain commands run in a container via **podman**, never docker.
- Frontend is **Next.js 16.2.10** static export (`output: "export"`), **React 19.2.7**, **TypeScript 5.9.3**, **Zustand 5.0.14**. Tests are **vitest 4.1.10**; E2E is **@playwright/test 1.61.1**.
- Media libraries are pinned: **epubjs 0.4.2**, **pdfjs-dist 6.1.200**, **hls.js 1.6.16**. Do not upgrade them as part of this work.
- `backend/` is one flat Go package (`module telos-core`). Extend the sibling file matching the concern; do not grow `main.go`.
- Schema changes are a **new numbered migration** in `backend/db/migrations/`. Highest existing is `0022_catalog_identity_and_progress.sql`. Migrations are idempotent (`IF NOT EXISTS` / `ON CONFLICT`). **Never edit an already-applied migration** — `backend/migrations.go` verifies checksums.
- All credentials come from `.env` interpolation. Never hardcode secrets.
- **Copyleft boundary:** never link or compile Jellyfin or Grimmory code into the Go gateway. Integrate over HTTP across container boundaries only.
- Never hand-edit `frontend/src/styles/tokens/` or `frontend/src/styles/foundations/`.
- Never alter the environment to make a gate pass. A failing gate is reported, not forced green.
- A local backend `go build` fails unless `backend/out/` exists, until S1's build tag lands.

## Verification commands — use these exact forms

```bash
# Backend unit + vet (containerized)
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go vet ./...

# Backend integration (selectors: auth|realtime|security|db|storage|product)
bash scripts/test-backend.sh auth

# Frontend (from frontend/)
npm run lint && npx tsc --noEmit && npm run test:unit && npm run build

# Repo guards
bash scripts/validate-compose.sh --env-file .env
bash scripts/check-product-truth.sh
```

---

## File structure

- Create `backend/frontend_embed.go` — `//go:build embedfrontend` implementation of `registerFrontend(mux)`.
- Create `backend/frontend_noembed.go` — `//go:build !embedfrontend` no-op stub of `registerFrontend(mux)`.
- Modify `backend/frontend_theme.go:1` — add `//go:build embedfrontend` build constraint header.
- Modify `backend/main.go:117-118,391-396,549` — strip hardcoded `//go:embed` and delegate mounting to `registerFrontend(mux)`.
- Modify `backend/Dockerfile:1-26` — add `ARG EMBED_FRONTEND=false` and conditional build steps.
- Modify `frontend/src/components/ThemeSync.tsx:21-24` — gate cookie setting on gateway-served origin.
- Create `backend/cmd/genopenapi/main.go` — AST-driven OpenAPI 3.1 generator parsing the `mux` registrations in `backend/main.go`. Lives inside the module because the repository root has no `go.mod`.
- Create `backend/api/openapi.json` — committed OpenAPI 3.1 JSON document describing all 108 backend API routes.
- Create `frontend/src/lib/generated/api-client.ts` — TypeScript types generated from `backend/api/openapi.json`.
- Modify `frontend/package.json` — add `generate:client` build script.
- Modify `.github/workflows/ci.yml` — add `check-headless-build` and `check-api-contract-drift` CI jobs.

---

### Task 1: Create tagged embed file pair and make headless mode the default

**Files:**
- Create: `backend/frontend_embed.go`
- Create: `backend/frontend_noembed.go`
- Modify: `backend/frontend_theme.go:1`
- Modify: `backend/main.go:117-118,391-396,549`
- Test: `backend/main_test.go`

**Interfaces:**
- Produces `registerFrontend(mux *http.ServeMux)` in both build configurations.
- `embedfrontend` build tag embeds `backend/out/` and mounts static HTTP handlers.
- Default `!embedfrontend` build tag operates headlessly without needing `backend/out/`.

- [ ] **Step 1: Write a failing test asserting headless build compilation without out/ directory**

Add a test in `backend/main_test.go` verifying that `registerFrontend` can be called without panicking when frontend assets are not embedded:

```go
func TestRegisterFrontendHeadless(t *testing.T) {
	mux := http.NewServeMux()
	registerFrontend(mux)

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	// In headless mode (!embedfrontend), GET / returns 404 Not Found since no frontend is mounted
	if rr.Code != http.StatusNotFound && rr.Code != http.StatusOK {
		t.Fatalf("expected status 404 or 200, got %d", rr.Code)
	}
}
```

- [ ] **Step 2: Run test to confirm compilation failure**

Run backend unit tests:

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./... -run TestRegisterFrontendHeadless
```

Expected: FAIL with compilation error because `registerFrontend` is not yet defined.

- [ ] **Step 3: Create backend/frontend_embed.go with embedfrontend build constraint**

Create `backend/frontend_embed.go`:

```go
//go:build embedfrontend

package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
)

//go:embed all:out
var frontendFS embed.FS

func registerFrontend(mux *http.ServeMux) {
	subFS, err := fs.Sub(frontendFS, "out")
	if err != nil {
		log.Fatalf("Failed to locate frontend out/ directory: %v", err)
	}
	fileServer := http.FileServer(http.FS(subFS))
	mux.Handle("/", themedFrontend(fileServer))
}
```

- [ ] **Step 4: Create backend/frontend_noembed.go with !embedfrontend build constraint**

Create `backend/frontend_noembed.go`:

```go
//go:build !embedfrontend

package main

import "net/http"

func registerFrontend(mux *http.ServeMux) {
	// Headless mode: zero frontend asset embedding or static asset serving.
}
```

- [ ] **Step 5: Add build constraint header to backend/frontend_theme.go**

Modify `backend/frontend_theme.go:1` to add the `//go:build embedfrontend` header:

```go
//go:build embedfrontend

package main

import (
	"bytes"
	"net/http"
	"path"
	"strconv"
	"strings"
)
```

- [ ] **Step 6: Refactor backend/main.go to remove hardcoded embed directives**

Remove `frontendFS` definition at `backend/main.go:117-118`:

```go
// Remove lines 117-118:
// //go:embed all:out
// var frontendFS embed.FS
```

Replace `subFS` initialization at `backend/main.go:391-396` and `mux.Handle("/", ...)` at `backend/main.go:549` with `registerFrontend(mux)`:

```go
	// Create ServeMux
	mux := http.NewServeMux()

	// Register frontend static file handler (no-op in headless mode)
	registerFrontend(mux)
```

- [ ] **Step 7: Verify load-bearing headless compilation without out/ directory**

Run backend unit tests in headless mode (without `backend/out/`):

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go vet ./...
```

Expected: PASS. The backend compiles and all tests pass in default headless mode.

- [ ] **Step 8: Verify embedded compilation with embedfrontend build tag**

Run backend unit tests with the `embedfrontend` build tag:

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test -tags embedfrontend ./...
```

Expected: PASS.

---

### Task 2: Update Dockerfile for optional frontend embedding

**Files:**
- Modify: `backend/Dockerfile:1-26`

**Interfaces:**
- Introduces build ARG `EMBED_FRONTEND=false` (default `false`) for building lightweight headless containers.

- [ ] **Step 1: Update backend/Dockerfile to accept EMBED_FRONTEND build argument**

Modify `backend/Dockerfile:1-26`:

```dockerfile
# Build the static frontend export
FROM node:24.18.0-alpine AS frontend
WORKDIR /fe
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# Build Go Backend
FROM golang:1.26.5-alpine AS builder
ARG EMBED_FRONTEND=false
WORKDIR /app
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./

# Conditionally copy frontend assets if EMBED_FRONTEND is true
COPY --from=frontend /fe/out ./out_temp
RUN if [ "$EMBED_FRONTEND" = "true" ]; then mv ./out_temp ./out; else rm -rf ./out_temp; fi

RUN if [ "$EMBED_FRONTEND" = "true" ]; then \
        CGO_ENABLED=0 GOOS=linux go build -tags embedfrontend -trimpath -o telos-core . ; \
    else \
        CGO_ENABLED=0 GOOS=linux go build -trimpath -o telos-core . ; \
    fi

# Final Image
FROM alpine:3.22.2
WORKDIR /app
COPY --from=builder /app/telos-core .
EXPOSE 8080
CMD ["./telos-core"]
```

- [ ] **Step 2: Validate container build command syntax**

Verify Dockerfile syntax using compose validation:

```bash
bash scripts/validate-compose.sh --env-file .env
```

---

### Task 3: Gate ThemeSync cookie writing on gateway origin

**Files:**
- Modify: `frontend/src/components/ThemeSync.tsx:21-24`

**Interfaces:**
- Gated theme cookie write: `ThemeSync` mirrors `telos_theme` cookie only when running in gateway-served web UI context.

- [ ] **Step 1: Update ThemeSync.tsx to gate cookie creation**

Modify `frontend/src/components/ThemeSync.tsx:21-24`:

```tsx
"use client";

import { useEffect } from "react";
import { useThemeStore } from "@/stores/useThemeStore";

export function ThemeSync() {
  const theme = useThemeStore((s) => s.theme);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;

    // Gate cookie stamping to same-origin web server environments.
    // Native apps and cross-origin clients do not send gateway theme cookies.
    if (typeof window !== "undefined" && window.location.protocol.startsWith("http")) {
      const secure = window.location.protocol === "https:" ? "; Secure" : "";
      const oneYear = 60 * 60 * 24 * 365;
      document.cookie = `telos_theme=${theme}; Path=/; Max-Age=${oneYear}; SameSite=Lax${secure}`;
    }
  }, [theme]);

  return null;
}
```

- [ ] **Step 2: Verify frontend lint and unit tests pass**

```bash
cd frontend
npm run lint
npm run test:unit
```

Expected: PASS.

---

### Task 4: Implement the AST-driven OpenAPI generator and commit backend/api/openapi.json

**Files:**
- Create: `backend/cmd/genopenapi/main.go`
- Create: `backend/cmd/genopenapi/main_test.go`
- Create: `backend/api/openapi.json`

**Interfaces:**
- Produces: `parseRoutes(sourceFile string) ([]Route, error)` and `type Route struct { Method, Path string }`.
- The generator **parses** `backend/main.go` with `go/ast`; it must never carry a hand-written route list. A hardcoded inventory would make the drift gate compare the hardcoded list against the JSON instead of comparing reality against the JSON, so adding a route to `main.go` would still pass. The route table is the single source of truth.
- Lives inside the `telos-core` module. The repository root has **no** `go.mod` — the only module is `backend/go.mod` — so a generator under `scripts/` could not be run with `go run` at all.
- Flags: `--check` (verify freshness, write nothing), `--source` (default `main.go`), `--out` (default `api/openapi.json`). Defaults assume the working directory is `backend/`.

- [ ] **Step 1: Write the failing test**

Create `backend/cmd/genopenapi/main_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseRoutesExtractsMethodAndPath(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "routes.go")
	fixture := `package main

func register() {
	mux.HandleFunc("GET /api/v1/health", handleHealth)
	mux.Handle("POST /api/v1/channels/{id}/pins", withAuth(nil, "manage_messages"))
	mux.Handle("/", frontend)
}
`
	if err := os.WriteFile(src, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}

	routes, err := parseRoutes(src)
	if err != nil {
		t.Fatalf("parseRoutes: %v", err)
	}
	// The unprefixed "/" catch-all serves the frontend and is not an API route.
	if len(routes) != 2 {
		t.Fatalf("want 2 routes, got %d: %+v", len(routes), routes)
	}
	// Results are sorted by path, then method.
	if routes[0].Method != "POST" || routes[0].Path != "/api/v1/channels/{id}/pins" {
		t.Errorf("routes[0] = %+v", routes[0])
	}
	if routes[1].Method != "GET" || routes[1].Path != "/api/v1/health" {
		t.Errorf("routes[1] = %+v", routes[1])
	}
}

func TestParseRoutesCoversEveryRegistrationInMainGo(t *testing.T) {
	routes, err := parseRoutes("../../main.go")
	if err != nil {
		t.Fatalf("parseRoutes: %v", err)
	}
	// 109 mux registrations exist; exactly one is the unprefixed catch-all.
	if len(routes) != 108 {
		t.Fatalf("want 108 API routes, got %d", len(routes))
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 \
  go test ./cmd/genopenapi/ -run TestParseRoutes -v
```

Expected: FAIL — `undefined: parseRoutes`.

- [ ] **Step 3: Implement the generator**

Create `backend/cmd/genopenapi/main.go`:

```go
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Route is one API registration discovered in the gateway's route table.
type Route struct {
	Method string
	Path   string
}

// parseRoutes walks sourceFile for mux.Handle and mux.HandleFunc calls and
// returns every API route registered. Patterns use the Go 1.22+ form
// "METHOD /path". The single unprefixed registration is the frontend
// catch-all and is deliberately excluded.
func parseRoutes(sourceFile string) ([]Route, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, sourceFile, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", sourceFile, err)
	}

	var routes []Route
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc" {
			return true
		}
		recv, ok := sel.X.(*ast.Ident)
		if !ok || recv.Name != "mux" {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		pattern, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		space := strings.IndexByte(pattern, ' ')
		if space <= 0 {
			return true // frontend catch-all
		}
		routes = append(routes, Route{
			Method: pattern[:space],
			Path:   strings.TrimSpace(pattern[space+1:]),
		})
		return true
	})

	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Path != routes[j].Path {
			return routes[i].Path < routes[j].Path
		}
		return routes[i].Method < routes[j].Method
	})
	return routes, nil
}

// operationID derives a stable, unique identifier from a route.
func operationID(r Route) string {
	trimmed := strings.TrimPrefix(r.Path, "/api/v1/")
	slug := strings.NewReplacer("/", "_", "{", "", "}", "", "-", "_").Replace(trimmed)
	return strings.ToLower(r.Method) + "_" + slug
}

// buildDocument renders the discovered routes as an OpenAPI 3.1 document.
// Go 1.22 wildcards ({id}) are already OpenAPI-shaped.
func buildDocument(routes []Route) map[string]any {
	paths := map[string]any{}
	for _, r := range routes {
		item, _ := paths[r.Path].(map[string]any)
		if item == nil {
			item = map[string]any{}
			paths[r.Path] = item
		}
		item[strings.ToLower(r.Method)] = map[string]any{
			"operationId": operationID(r),
			"responses": map[string]any{
				"200": map[string]any{"description": "Success"},
			},
		}
	}
	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       "Telos Gateway API",
			"version":     "1.0.0",
			"description": "Generated from the mux route table in main.go. Do not edit by hand.",
		},
		"paths": paths,
	}
}

func main() {
	check := flag.Bool("check", false, "verify the committed document is current; write nothing")
	source := flag.String("source", "main.go", "route table source file")
	out := flag.String("out", filepath.Join("api", "openapi.json"), "output path")
	flag.Parse()

	routes, err := parseRoutes(*source)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Fail loudly rather than emitting an empty contract if the walk breaks.
	if len(routes) == 0 {
		fmt.Fprintf(os.Stderr, "no routes found in %s - refusing to write an empty contract\n", *source)
		os.Exit(1)
	}

	encoded, err := json.MarshalIndent(buildDocument(routes), "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoded = append(encoded, '\n')

	if *check {
		existing, err := os.ReadFile(*out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cannot read %s: %v\n", *out, err)
			os.Exit(1)
		}
		if string(existing) != string(encoded) {
			fmt.Fprintf(os.Stderr, "OpenAPI drift: %s is stale. Run: cd backend && go run ./cmd/genopenapi\n", *out)
			os.Exit(1)
		}
		fmt.Printf("OpenAPI contract is current (%d routes).\n", len(routes))
		return
	}

	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, encoded, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("Wrote %s (%d routes).\n", *out, len(routes))
}
```

- [ ] **Step 4: Run the test to verify it passes**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 \
  go test ./cmd/genopenapi/ -run TestParseRoutes -v
```

Expected: PASS, both tests.

- [ ] **Step 5: Generate the committed contract**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 \
  go run ./cmd/genopenapi
```

Expected: `Wrote api/openapi.json (108 routes).`

- [ ] **Step 6: Verify the drift check passes on fresh output**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 \
  go run ./cmd/genopenapi --check
```

Expected: exit 0, `OpenAPI contract is current (108 routes).`

- [ ] **Step 7: Verify the drift check actually catches drift**

This step proves the gate works. Temporarily add a throwaway route to `backend/main.go`
immediately after the health registrations, re-run `--check`, then revert.

```bash
sed -i 's|\tmux.HandleFunc("GET /api/v1/health", handleReadiness)|\tmux.HandleFunc("GET /api/v1/health", handleReadiness)\n\tmux.HandleFunc("GET /api/v1/drift-probe", handleReadiness)|' backend/main.go
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 \
  go run ./cmd/genopenapi --check
```

Expected: exit 1, `OpenAPI drift: api/openapi.json is stale.`

```bash
git checkout backend/main.go
```

- [ ] **Step 8: Commit**

```bash
git add backend/cmd/genopenapi/main.go backend/cmd/genopenapi/main_test.go backend/api/openapi.json
git commit -m "feat: generate the OpenAPI contract from the gateway route table"
```

### Task 5: Generate TypeScript client into frontend/src/lib/generated/

**Files:**
- Create: `frontend/src/lib/generated/api-client.ts`
- Modify: `frontend/package.json`

**Interfaces:**
- Generates type-safe TypeScript interfaces from `backend/api/openapi.json` into `frontend/src/lib/generated/api-client.ts`.

- [ ] **Step 1: Update frontend/package.json with generation script**

Add `generate:client` script to `frontend/package.json`:

```json
"scripts": {
  "generate:client": "openapi-typescript ../backend/api/openapi.json -o src/lib/generated/api-client.ts"
}
```

- [ ] **Step 2: Execute client generation**

Run generation script:

```bash
cd frontend
npx openapi-typescript ../backend/api/openapi.json -o src/lib/generated/api-client.ts
```

Verify `frontend/src/lib/generated/api-client.ts` is created and contains TypeScript type definitions for all API endpoints.

- [ ] **Step 3: Run typecheck-without-backend gate**

Verify frontend typechecking succeeds against the generated client without connecting to a backend container:

```bash
cd frontend
npx tsc --noEmit
```

Expected: PASS with 0 errors.

---

### Task 6: Add CI verification jobs for contract drift and headless compilation

**Files:**
- Modify: `.github/workflows/ci.yml`

**Interfaces:**
- CI job `check-headless-build`: verifies backend builds and passes tests with `frontend/` directory absent.
- CI job `check-api-contract-drift`: fails if `backend/api/openapi.json` is stale.

- [ ] **Step 1: Add check-headless-build and check-api-contract-drift jobs to .github/workflows/ci.yml**

Modify `.github/workflows/ci.yml`:

```yaml
  check-headless-build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26.5'
      - name: Verify Backend Builds Headless (No Frontend Directory)
        run: |
          rm -rf frontend
          cd backend
          go test -v ./...
          go build -v -o /tmp/telos-core-headless .

  check-api-contract-drift:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26.5'
      - name: Check OpenAPI Spec Freshness
        run: |
          cd backend && go run ./cmd/genopenapi --check
```

- [ ] **Step 2: Run full verification suite**

Run all repository verification gates:

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go vet ./...
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test -tags embedfrontend ./...
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go run ./cmd/genopenapi --check

cd frontend
npm run lint && npx tsc --noEmit && npm run test:unit && npm run build

bash scripts/validate-compose.sh --env-file .env
bash scripts/check-product-truth.sh
```

Expected: All commands PASS.
