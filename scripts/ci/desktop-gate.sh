#!/usr/bin/env bash
# desktop-gate.sh <gate> — the Phase 3 local gates, one entry point per catalog id.
#
#   device-registration-boundary  public password registration, exact-origin CORS, bearer logout (real PG/Redis)
#   native-auth-state             the cookie/token auth state machine and its API-client integration
#   os-credential-adapter         the keyring bridge (Rust fmt/clippy/test) and the artifact verifier
#   platform-trusted-tls          HTTPS-only connection, no pinning or override, product-truth scan
#   token-safe-media              revocable capability URLs on the server and in the players
#
# A missing prerequisite (Podman for the PG/Redis harness, WebKitGTK headers for the
# Rust shell) is `not_run` (exit 3), never a pass. Named backend cases must execute:
# a SKIP in their output fails the gate.
set -euo pipefail

gate="${1:-}"
repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

not_run() { echo "not_run: $*" >&2; exit 3; }
need() { command -v "$1" >/dev/null 2>&1 || not_run "$1 is required for $gate"; }

vitest() { (cd frontend && npx --no-install vitest run "$@"); }
typecheck() { (cd frontend && npx --no-install tsc --noEmit); }

# backend_named PATTERN — runs matching Go tests against real PostgreSQL and Redis and
# requires at least one to pass and none to be skipped.
backend_named() {
  need podman
  local log
  log="$(mktemp)"
  trap 'rm -f "$log"' RETURN
  bash scripts/test-backend.sh all -count=1 -v -run "$1" 2>&1 | tee "$log"
  if grep -q -- '--- SKIP' "$log"; then
    echo "$gate: a named backend case was skipped" >&2
    return 1
  fi
  grep -q -- '--- PASS' "$log" || { echo "$gate: no named backend case ran" >&2; return 1; }
}

rust_checks() {
  need cargo
  pkg-config --exists webkit2gtk-4.1 2>/dev/null || not_run "WebKitGTK 4.1 development files (libwebkit2gtk-4.1-dev) are required to build the Tauri shell"
  (cd frontend/src-tauri && cargo fmt --check && cargo clippy --locked --all-targets -- -D warnings && cargo test --locked)
}

case "$gate" in
  device-registration-boundary)
    backend_named 'TestProductionDeviceRegistration|TestCORSPreflight|TestPublicRoutes|TestDeviceRegistrationIsRegistered|TestBearerLogoutRevokesTheDevice|TestMeNeverSerializesTheSessionHash'
    need podman
    bash scripts/test-backend.sh auth
    bash scripts/test-backend.sh security
    ;;
  native-auth-state)
    vitest src/lib/deviceAuth.test.ts src/stores/useAuthStore.test.ts src/stores/useAuthStore.native.test.ts \
      src/stores/useConnectionStore.test.ts src/lib/api_auth.test.ts
    typecheck
    ;;
  os-credential-adapter)
    vitest src/lib/secureStorage.test.ts src/lib/nativeShellContract.test.ts
    bash tests/operations/desktop_artifact_test.sh
    python3 -I tests/operations/desktop_workflow_contract_test.py
    rust_checks
    ;;
  platform-trusted-tls)
    vitest src/app/connect/connect_page.test.tsx src/lib/serverConfig.test.ts src/lib/nativeShellContract.test.ts \
      src/stores/useConnectionStore.test.ts
    bash scripts/check-product-truth.sh
    ;;
  token-safe-media)
    backend_named 'TestHLS|TestStreamTicket|TestMediaLocator'
    vitest src/lib/mediaLocator.test.ts src/components/stream/HlsPlayer.test.tsx src/components/AuthenticatedImage.test.tsx
    (cd frontend && node scripts/check-origin-leaks.js)
    ;;
  *)
    echo "usage: $0 device-registration-boundary|native-auth-state|os-credential-adapter|platform-trusted-tls|token-safe-media" >&2
    exit 2
    ;;
esac
echo "$gate: verified"
