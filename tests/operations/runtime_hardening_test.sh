#!/usr/bin/env bash
set -euo pipefail

# Static Compose / Traefik topology contract (Phase 2 Task 2) plus the existing
# hardening-document check. Compose rendering uses the hermetic test double in
# tests/helpers/compose_double unless TELOS_COMPOSE_REAL=1 selects the host's own
# `podman compose`; a real node or CI run must pass with a real provider.
repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

echo "Running runtime hardening test suite..."
if [[ ! -f "documentation/operations/container-hardening.md" ]]; then
  echo "container-hardening.md missing"
  exit 1
fi

if [[ "${TELOS_COMPOSE_REAL:-0}" != 1 ]]; then
  export PATH="$repo_root/tests/helpers/compose_double:$PATH"
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

make_fixture() {
  local fixture="$1"
  mkdir -p "$fixture/config/dynamic" "$fixture/tests/fixtures"
  cp "$repo_root/docker-compose.yml" "$fixture/docker-compose.yml"
  cp "$repo_root/config/dynamic/routes.yaml" "$fixture/config/dynamic/routes.yaml"
  cp "$repo_root/tests/fixtures/compose.env" "$fixture/tests/fixtures/compose.env"
  cp "$repo_root/docker-compose.setup.yml" "$fixture/docker-compose.setup.yml"
}

run_checks() {
  local fixture="$1"
  bash "$repo_root/scripts/validate-compose.sh" --root "$fixture" \
    --env-file tests/fixtures/compose.env --checks standalone-client,route-split 2>&1
}

assert_accepted() {
  local fixture="$1" output
  output="$(run_checks "$fixture")" || { echo "rejected the valid topology:" >&2; echo "$output" >&2; exit 1; }
}

assert_rejected() {
  local label="$1" fixture="$2" fragment="$3" output status
  set +e
  output="$(run_checks "$fixture")"
  status=$?
  set -e
  [[ $status -ne 0 ]] || { echo "accepted invalid topology: $label" >&2; echo "$output" >&2; exit 1; }
  grep -Fq -- "$fragment" <<<"$output" || {
    echo "mutation $label did not report '$fragment':" >&2
    echo "$output" >&2
    exit 1
  }
}

mutate_compose() {
  local fixture="$1" mutation="$2"
  python3 - "$fixture/docker-compose.yml" "$mutation" <<'PY'
import re
import sys

path, mutation = sys.argv[1:]
text = open(path, encoding="utf-8").read()
start = text.index("  telos-client:\n")
end = text.index("\n  postgres:\n") + 1
block = text[start:end]
if mutation == "backend-network":
    block = block.replace("      - telos-ingress\n", "      - telos-ingress\n      - telos-backend\n", 1)
elif mutation == "db-network-only":
    block = block.replace("      - telos-ingress\n", "      - telos-db\n", 1)
elif mutation == "published-port":
    block = block.replace("    networks:\n", "    ports:\n      - \"8088:8080\"\n    networks:\n", 1)
elif mutation == "secret-env":
    block = block.replace("    networks:\n", "    environment:\n      - DATABASE_URL=postgres://x\n    networks:\n", 1)
elif mutation == "token-env":
    block = block.replace("    networks:\n", "    environment:\n      - JELLYFIN_ADMIN_TOKEN=${JELLYFIN_ADMIN_TOKEN}\n    networks:\n", 1)
elif mutation == "volume":
    block = block.replace("    networks:\n", "    volumes:\n      - ${STORAGE_PATH:-/mnt/storage/shared}:/data/shared:z\n    networks:\n", 1)
elif mutation == "writable-root":
    block = block.replace("    read_only: true\n", "", 1)
elif mutation == "keeps-capabilities":
    block = block.replace("    cap_drop:\n      - ALL\n", "", 1)
elif mutation == "no-healthcheck":
    block = re.sub(r"    healthcheck:.*", "", block, flags=re.S)
elif mutation == "depends-on-core":
    block = block.replace("    networks:\n", "    depends_on:\n      - telos-core\n    networks:\n", 1)
elif mutation == "wrong-build":
    block = block.replace("context: ./frontend", "context: ./backend", 1)
elif mutation == "no-privileges":
    block = block.replace("      - no-new-privileges:true\n", "      - label=disable\n", 1)
elif mutation == "core-embedded":
    text = text.replace("  telos-core:\n    build:\n      context: .\n      dockerfile: backend/Dockerfile\n",
                        "  telos-core:\n    build:\n      context: .\n      dockerfile: backend/Dockerfile\n      args:\n        EMBED_FRONTEND: \"true\"\n", 1)
    start = text.index("  telos-client:\n")
    end = text.index("\n  postgres:\n") + 1
    block = text[start:end]
elif mutation == "core-published":
    text = text.replace("  telos-core:\n    build:", "  telos-core:\n    ports:\n      - \"8080:8080\"\n    build:", 1)
    start = text.index("  telos-client:\n")
    end = text.index("\n  postgres:\n") + 1
    block = text[start:end]
elif mutation == "core-loses-db":
    text = text.replace("      - telos-backend\n      - telos-db\n    environment:\n      # Least-privilege", "      - telos-backend\n    environment:\n      # Least-privilege", 1)
    start = text.index("  telos-client:\n")
    end = text.index("\n  postgres:\n") + 1
    block = text[start:end]
elif mutation == "remove":
    block = ""
else:
    raise SystemExit(f"unknown mutation {mutation}")
open(path, "w", encoding="utf-8").write(text[:start] + block + text[end:])
PY
}

mutate_routes() {
  local fixture="$1" mutation="$2"
  python3 - "$fixture/config/dynamic/routes.yaml" "$mutation" <<'PY'
import re
import sys

path, mutation = sys.argv[1:]
text = open(path, encoding="utf-8").read()


def router_block(name):
    match = re.search(rf"^    {name}:\n(?:        .*\n|      .*\n|\n)*?(?=^    \S|^  \S|\Z)", text, re.M)
    return match


if mutation == "auth-to-client":
    text = re.sub(r"(    telos-auth:\n(?:      .*\n|        .*\n)*?      service: )telos-core", r"\1telos-client", text, count=1)
elif mutation == "streaming-to-client":
    text = re.sub(r"(    telos-streaming:\n(?:      .*\n|        .*\n)*?      service: )telos-core", r"\1telos-client", text, count=1)
elif mutation == "catch-all-to-core":
    text = re.sub(r"(    telos-web:\n(?:      .*\n|        .*\n)*?      service: )telos-client", r"\1telos-core", text, count=1)
elif mutation == "no-api-fallback":
    text = re.sub(r"    telos-api:\n(?:      .*\n|        .*\n)*\n", "", text, count=1)
elif mutation == "api-fallback-low-priority":
    text = re.sub(r"(    telos-api:\n(?:      .*\n|        .*\n)*?      priority: )100", r"\g<1>1", text, count=1)
elif mutation == "client-wrong-url":
    text = text.replace("http://telos-client:8080", "http://telos-client:9000", 1)
elif mutation == "client-service-missing":
    text = re.sub(r"    telos-client:\n      loadBalancer:\n        servers:\n          - url: http://telos-client:8080\n", "", text)
elif mutation == "core-wrong-url":
    text = text.replace("http://telos-core:8080", "http://telos-core:9000", 1)
elif mutation == "two-catch-alls":
    text = text.replace("    telos-api:\n", "    telos-extra:\n      rule: 'Host(`{{ env \"TELOS_DOMAIN\" }}`) && PathPrefix(`/`)'\n      priority: 1\n      service: telos-core\n\n    telos-api:\n", 1)
elif mutation == "web-outranks-api":
    text = re.sub(r"(    telos-web:\n(?:      .*\n|        .*\n)*?      priority: )1", r"\g<1>500", text, count=1)
else:
    raise SystemExit(f"unknown mutation {mutation}")
open(path, "w", encoding="utf-8").write(text)
PY
}

valid="$work/valid"
make_fixture "$valid"
assert_accepted "$valid"

for mutation in backend-network db-network-only published-port secret-env token-env volume writable-root \
  keeps-capabilities no-healthcheck depends-on-core wrong-build no-privileges remove; do
  fixture="$work/compose-$mutation"
  make_fixture "$fixture"
  mutate_compose "$fixture" "$mutation"
  assert_rejected "compose:$mutation" "$fixture" "standalone-client"
done
for mutation in core-embedded core-published core-loses-db; do
  fixture="$work/compose-$mutation"
  make_fixture "$fixture"
  mutate_compose "$fixture" "$mutation"
  assert_rejected "compose:$mutation" "$fixture" "telos-core"
done

for mutation in auth-to-client streaming-to-client catch-all-to-core no-api-fallback api-fallback-low-priority \
  client-wrong-url client-service-missing core-wrong-url two-catch-alls web-outranks-api; do
  fixture="$work/routes-$mutation"
  make_fixture "$fixture"
  mutate_routes "$fixture" "$mutation"
  assert_rejected "routes:$mutation" "$fixture" "route-split"
done

run_overlay_check() {
  bash "$repo_root/scripts/validate-compose.sh" --root "$1" --env-file tests/fixtures/compose.env --checks setup-overlay 2>&1
}
output="$(run_overlay_check "$valid")" || { echo "the real setup overlay was rejected: $output" >&2; exit 1; }
overlay_variants=(
  '      - "0.0.0.0:${JELLYFIN_SETUP_PORT:-8096}:8096"'
  '      - "${JELLYFIN_SETUP_PORT:-8096}:8096"'
  '      - "8096:8096"'
  '      - "127.0.0.1:${JELLYFIN_SETUP_PORT:-8096}:8096"\n      - "127.0.0.1:9000:9000"'
  '      - "[::]:${JELLYFIN_SETUP_PORT:-8096}:8096"'
  '      - "127.0.0.2:${JELLYFIN_SETUP_PORT:-8096}:8096"'
)
for index in "${!overlay_variants[@]}"; do
  fixture="$work/overlay-$index"
  make_fixture "$fixture"
  python3 - "$fixture/docker-compose.setup.yml" "${overlay_variants[$index]}" <<'PY'
import sys
path, replacement = sys.argv[1:]
text = open(path, encoding="utf-8").read()
text = text.replace('      - "127.0.0.1:${JELLYFIN_SETUP_PORT:-8096}:8096"', replacement.replace("\\n", "\n"), 1)
open(path, "w", encoding="utf-8").write(text)
PY
  set +e; output="$(run_overlay_check "$fixture")"; status=$?; set -e
  [[ $status -ne 0 ]] || { echo "setup overlay variant $index was accepted" >&2; exit 1; }
  grep -Fq setup-overlay <<<"$output" || { echo "variant $index did not name setup-overlay: $output" >&2; exit 1; }
done
fixture="$work/overlay-extra-service"
make_fixture "$fixture"
printf '  postgres:\n    ports:\n      - "127.0.0.1:5432:5432"\n' >>"$fixture/docker-compose.setup.yml"
set +e; output="$(run_overlay_check "$fixture")"; status=$?; set -e
[[ $status -ne 0 ]] || { echo "a setup overlay touching postgres was accepted" >&2; exit 1; }
fixture="$work/overlay-production-port"
make_fixture "$fixture"
python3 - "$fixture/docker-compose.yml" <<'PY'
import sys
path = sys.argv[1]
text = open(path, encoding="utf-8").read()
open(path, "w", encoding="utf-8").write(text.replace("  jellyfin:\n    image: jellyfin/jellyfin:latest\n", "  jellyfin:\n    ports:\n      - \"8096:8096\"\n    image: jellyfin/jellyfin:latest\n", 1))
PY
set +e; output="$(run_overlay_check "$fixture")"; status=$?; set -e
[[ $status -ne 0 ]] || { echo "a production model publishing Jellyfin was accepted" >&2; exit 1; }
rm -f "$fixture/docker-compose.setup.yml"
set +e; output="$(run_overlay_check "$fixture")"; status=$?; set -e
[[ $status -ne 0 ]] || { echo "a missing overlay was accepted" >&2; exit 1; }

# An unknown check must be a usage error, never a silent pass.
set +e
bash "$repo_root/scripts/validate-compose.sh" --root "$valid" --env-file tests/fixtures/compose.env --checks no-such-check >/dev/null 2>&1
status=$?
set -e
[[ $status -eq 2 ]] || { echo "unknown check was not a usage error (exit $status)" >&2; exit 1; }

# The pre-existing checks still pass on the live model.
bash "$repo_root/scripts/validate-compose.sh" --env-file tests/fixtures/compose.env >/dev/null

# No provider at all is not_run, not a verdict.
set +e
bash_bin="$(command -v bash)"
PATH="/nonexistent" "$bash_bin" "$repo_root/scripts/validate-compose.sh" --root "$valid" \
  --env-file tests/fixtures/compose.env >/dev/null 2>&1
status=$?
set -e
[[ $status -eq 3 ]] || { echo "missing podman was not not_run (exit $status)" >&2; exit 1; }

echo "Runtime hardening test suite passed."
