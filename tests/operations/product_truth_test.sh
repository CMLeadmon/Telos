#!/usr/bin/env bash
set -euo pipefail

# The production change this test protects is a checker that merely scans words
# instead of deriving the beta boundary from its source inventories. Each
# mutation below changes one real input to the checker and must be rejected.

repo_root="$(git rev-parse --show-toplevel)"
fixture_root="$(mktemp -d)"
trap 'rm -rf "$fixture_root"' EXIT

copy_fixture() {
  local fixture="$1"
  local relative
  local -a files=(
    backend/main.go
    ci/phase-gates.json
    config/dynamic/routes.yaml
    docker-compose.yml
    documentation/product/beta-feature-status.md
    frontend/package.json
    frontend/package-lock.json
    frontend/src/components/AppShell.tsx
    backend/go.mod
    backend/go.sum
  )

  rm -rf "$fixture"
  mkdir -p "$fixture"
  for relative in "${files[@]}"; do
    mkdir -p "$(dirname "$fixture/$relative")"
    cp "$repo_root/$relative" "$fixture/$relative"
  done
  cp "$repo_root/LICENSE" "$fixture/LICENSE"
  cp "$repo_root/README.md" "$fixture/README.md"
  mkdir -p "$fixture/documentation/product"
  cp "$repo_root/documentation/product/beta-contract.json" \
    "$fixture/documentation/product/beta-contract.json"
}

assert_rejected() {
  local mutation="$1"
  local field="$2"
  local fixture="$3"
  local output

  if output="$(bash "$repo_root/scripts/check-product-truth.sh" --root "$fixture" 2>&1)"; then
    echo "accepted invalid product fixture: $mutation" >&2
    exit 1
  fi
  if ! grep -Fq "$field" <<<"$output"; then
    echo "mutation $mutation did not name violated field $field:" >&2
    echo "$output" >&2
    exit 1
  fi
}

clean="$fixture_root/clean"
copy_fixture "$clean"
bash "$repo_root/scripts/check-product-truth.sh" --root "$clean"

contract_modules="$fixture_root/contract-modules"
copy_fixture "$contract_modules"
python3 - "$contract_modules/documentation/product/beta-contract.json" <<'PY'
import json
import sys

path = sys.argv[1]
with open(path, encoding="utf-8") as source:
    contract = json.load(source)
contract["modules"].remove("files")
with open(path, "w", encoding="utf-8") as destination:
    json.dump(contract, destination, indent=2)
    destination.write("\n")
PY
assert_rejected "contract-modules" "modules" "$contract_modules"

voice_module="$fixture_root/voice-module"
copy_fixture "$voice_module"
python3 - "$voice_module/frontend/src/components/AppShell.tsx" <<'PY'
import sys

path = sys.argv[1]
with open(path, encoding="utf-8") as source:
    text = source.read()
needle = "const MODULES = ["
replacement = needle + '\n  { href: "/voice", label: "Voice", icon: MessageSquare, capability: "view_voice" },'
if needle not in text:
    raise SystemExit("MODULES fixture anchor is missing")
with open(path, "w", encoding="utf-8") as destination:
    destination.write(text.replace(needle, replacement, 1))
PY
assert_rejected "voice-module" "modules" "$voice_module"

livekit_service="$fixture_root/livekit-service"
copy_fixture "$livekit_service"
python3 - "$livekit_service/docker-compose.yml" <<'PY'
import sys

path = sys.argv[1]
with open(path, encoding="utf-8") as source:
    text = source.read()
needle = "services:\n"
replacement = needle + "  livekit:\n    image: livekit/livekit:latest\n"
if needle not in text:
    raise SystemExit("Compose services fixture anchor is missing")
with open(path, "w", encoding="utf-8") as destination:
    destination.write(text.replace(needle, replacement, 1))
PY
assert_rejected "livekit-service" "removedFeatures" "$livekit_service"

livekit_dependency="$fixture_root/livekit-dependency"
copy_fixture "$livekit_dependency"
python3 - "$livekit_dependency/frontend/package.json" <<'PY'
import json
import sys

path = sys.argv[1]
with open(path, encoding="utf-8") as source:
    package = json.load(source)
package["dependencies"]["@livekit/components-react"] = "1.0.0"
with open(path, "w", encoding="utf-8") as destination:
    json.dump(package, destination, indent=2)
    destination.write("\n")
PY
assert_rejected "livekit-dependency" "removedFeatures" "$livekit_dependency"

browser_shipping="$fixture_root/browser-shipping"
copy_fixture "$browser_shipping"
python3 - "$browser_shipping/documentation/product/beta-feature-status.md" <<'PY'
import sys

path = sys.argv[1]
with open(path, encoding="utf-8") as source:
    text = source.read()
blocked = "Hosted-browser delivery is blocked until Phase 2."
if blocked not in text:
    raise SystemExit("hosted-browser blocked marker is missing")
with open(path, "w", encoding="utf-8") as destination:
    destination.write(text.replace(blocked, "Hosted-browser delivery is Shipping.", 1))
PY
assert_rejected "browser-shipping" "browserService" "$browser_shipping"

missing_gate="$fixture_root/missing-gate"
copy_fixture "$missing_gate"
python3 - "$missing_gate/ci/phase-gates.json" <<'PY'
import json
import sys

path = sys.argv[1]
with open(path, encoding="utf-8") as source:
    catalog = json.load(source)
catalog["gates"] = [gate for gate in catalog["gates"] if gate["id"] != "browser-matrix"]
with open(path, "w", encoding="utf-8") as destination:
    json.dump(catalog, destination, indent=2)
    destination.write("\n")
PY
assert_rejected "missing-gate" "requiredExternalGates" "$missing_gate"

backup_shipping="$fixture_root/backup-shipping"
copy_fixture "$backup_shipping"
python3 - "$backup_shipping/documentation/product/beta-feature-status.md" <<'PY'
import sys

path = sys.argv[1]
with open(path, encoding="utf-8") as source:
    text = source.read()
blocked = "Encrypted off-node backup and recovery are blocked until Phase 4 evidence exists."
if blocked not in text:
    raise SystemExit("backup blocked marker is missing")
with open(path, "w", encoding="utf-8") as destination:
    destination.write(text.replace(blocked, "Encrypted off-node backup and recovery are Shipping.", 1))
PY
assert_rejected "backup-shipping" "backupStatus" "$backup_shipping"

outside_root="$fixture_root/outside-root"
copy_fixture "$outside_root"
ln -s /etc/hostname "$outside_root/frontend/src/outside-root.ts"
assert_rejected "outside-root" "root" "$outside_root"
