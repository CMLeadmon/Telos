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

outside_directory="$fixture_root/outside-directory"
copy_fixture "$outside_directory"
ln -s /etc "$outside_directory/frontend/src/outside-directory"
assert_rejected "outside-directory" "root" "$outside_directory"

voice_route="$fixture_root/voice-route"
copy_fixture "$voice_route"
printf '\nmux.HandleFunc("GET /api/v1/voice/rooms", func(http.ResponseWriter, *http.Request) {})\n' \
  >>"$voice_route/backend/main.go"
assert_rejected "voice-route" "removedFeatures" "$voice_route"

notifications_route="$fixture_root/notifications-route"
copy_fixture "$notifications_route"
printf '\nmux.HandleFunc("GET /api/v1/notifications", func(http.ResponseWriter, *http.Request) {})\n' \
  >>"$notifications_route/backend/main.go"
assert_rejected "notifications-route" "removedFeatures" "$notifications_route"

realize_client() {
  local fixture="$1"
  python3 - "$fixture/docker-compose.yml" "$fixture/config/dynamic/routes.yaml" \
    "$fixture/documentation/product/beta-feature-status.md" <<'PY'
import sys

compose_path, routes_path, status_path = sys.argv[1:]
with open(compose_path, encoding="utf-8") as source:
    compose = source.read()
with open(compose_path, "w", encoding="utf-8") as destination:
    destination.write(compose + "\n  telos-client:\n    image: telos-client:fixture\n")
with open(routes_path, encoding="utf-8") as source:
    routes = source.read()
routes = routes.replace("      service: telos-core\n      tls:\n        certResolver: letsencrypt\n\n  middlewares:", "      service: telos-client\n      tls:\n        certResolver: letsencrypt\n\n  middlewares:", 1)
routes = routes.replace(
    "    # Default: other JSON API and the static frontend, 64 KiB edge limit.",
    """    telos-api:
      rule: 'Host(`telos`) && PathPrefix(`/api/v1`)'
      entryPoints:
        - websecure
      priority: 2
      middlewares:
        - telos-inflight
        - telos-json-body
      service: telos-core
      tls:
        certResolver: letsencrypt

    # Default: other JSON API and the static frontend, 64 KiB edge limit.""",
    1,
)
routes += "\n    telos-client:\n      loadBalancer:\n        servers:\n          - url: http://telos-client:8080\n"
with open(routes_path, "w", encoding="utf-8") as destination:
    destination.write(routes)
with open(status_path, encoding="utf-8") as source:
    status = source.read()
with open(status_path, "w", encoding="utf-8") as destination:
    destination.write(
        status.replace("Hosted-browser delivery is blocked until Phase 2.\n", "", 1)
        .replace("| Hosted-browser delivery | Blocked until Phase 2 |\n", "", 1)
        .replace("Hosted-browser delivery remains unavailable until that service and the Traefik\nroute split exist.\n", "", 1)
        .replace(
            "| Delivery boundary | Status |\n|---|---|\n",
            "| Delivery boundary | Status |\n|---|---|\n| Hosted-browser delivery | implemented-awaiting-evidence |\n",
            1,
        )
    )
PY
}

assert_accepted() {
  local fixture="$1"
  if ! bash "$repo_root/scripts/check-product-truth.sh" --root "$fixture"; then
    echo "rejected valid product fixture: $fixture" >&2
    exit 1
  fi
}

realized_client="$fixture_root/realized-client"
copy_fixture "$realized_client"
realize_client "$realized_client"
assert_accepted "$realized_client"

client_with_marker="$fixture_root/client-with-marker"
copy_fixture "$client_with_marker"
realize_client "$client_with_marker"
printf '\nHosted-browser delivery is blocked until Phase 2.\n' \
  >>"$client_with_marker/documentation/product/beta-feature-status.md"
assert_rejected "client-with-marker" "browserService" "$client_with_marker"

client_core_catchall="$fixture_root/client-core-catchall"
copy_fixture "$client_core_catchall"
realize_client "$client_core_catchall"
python3 - "$client_core_catchall/config/dynamic/routes.yaml" <<'PY'
import sys

path = sys.argv[1]
with open(path, encoding="utf-8") as source:
    text = source.read()
with open(path, "w", encoding="utf-8") as destination:
    destination.write(text.replace("      service: telos-client", "      service: telos-core", 1))
PY
assert_rejected "client-core-catchall" "browserService" "$client_core_catchall"

client_missing_api="$fixture_root/client-missing-api"
copy_fixture "$client_missing_api"
realize_client "$client_missing_api"
python3 - "$client_missing_api/config/dynamic/routes.yaml" <<'PY'
import sys

path = sys.argv[1]
with open(path, encoding="utf-8") as source:
    text = source.read()
start = text.index("    telos-api:")
end = text.index("    # Default", start)
with open(path, "w", encoding="utf-8") as destination:
    destination.write(text[:start] + text[end:])
PY
assert_rejected "client-missing-api" "apiService" "$client_missing_api"

client_api_wrong_target="$fixture_root/client-api-wrong-target"
copy_fixture "$client_api_wrong_target"
realize_client "$client_api_wrong_target"
python3 - "$client_api_wrong_target/config/dynamic/routes.yaml" <<'PY'
import sys

path = sys.argv[1]
with open(path, encoding="utf-8") as source:
    text = source.read()
start = text.index("    telos-api:")
end = text.index("    # Default", start)
block = text[start:end].replace("      service: telos-core", "      service: telos-client")
with open(path, "w", encoding="utf-8") as destination:
    destination.write(text[:start] + block + text[end:])
PY
assert_rejected "client-api-wrong-target" "apiService" "$client_api_wrong_target"

client_api_low_priority="$fixture_root/client-api-low-priority"
copy_fixture "$client_api_low_priority"
realize_client "$client_api_low_priority"
sed -i '/telos-api:/,/telos-core/ s/priority: 2/priority: 1/' \
  "$client_api_low_priority/config/dynamic/routes.yaml"
assert_rejected "client-api-low-priority" "apiService" "$client_api_low_priority"

livekit_traefik="$fixture_root/livekit-traefik"
copy_fixture "$livekit_traefik"
python3 - "$livekit_traefik/config/dynamic/routes.yaml" <<'PY'
import sys

path = sys.argv[1]
with open(path, encoding="utf-8") as source:
    text = source.read()
text = text.replace("\n  middlewares:", "\n    livekit:\n      rule: 'Host(livekit)'\n      service: telos-core\n\n  middlewares:", 1)
with open(path, "w", encoding="utf-8") as destination:
    destination.write(text)
PY
assert_rejected "livekit-traefik" "removedFeatures" "$livekit_traefik"

api_wrong_target="$fixture_root/api-wrong-target"
copy_fixture "$api_wrong_target"
python3 - "$api_wrong_target/config/dynamic/routes.yaml" <<'PY'
import sys

path = sys.argv[1]
with open(path, encoding="utf-8") as source:
    text = source.read()
with open(path, "w", encoding="utf-8") as destination:
    destination.write(text.replace("      service: telos-core", "      service: telos-client", 1))
PY
assert_rejected "api-wrong-target" "apiService" "$api_wrong_target"

client_wrong_url="$fixture_root/client-wrong-url"
copy_fixture "$client_wrong_url"
realize_client "$client_wrong_url"
sed -i 's|http://telos-client:8080|http://telos-core:8080|' \
  "$client_wrong_url/config/dynamic/routes.yaml"
assert_rejected "client-wrong-url" "browserService" "$client_wrong_url"

client_extra_url="$fixture_root/client-extra-url"
copy_fixture "$client_extra_url"
realize_client "$client_extra_url"
printf '          - url: http://telos-client:8081\n' \
  >>"$client_extra_url/config/dynamic/routes.yaml"
assert_rejected "client-extra-url" "browserService" "$client_extra_url"

settings_surface="$fixture_root/settings-surface"
copy_fixture "$settings_surface"
sed -i 's|href="/settings"|href="/preferences"|' "$settings_surface/frontend/src/components/AppShell.tsx"
assert_rejected "settings-surface" "settingsSurface" "$settings_surface"

desktop_marker="$fixture_root/desktop-marker"
copy_fixture "$desktop_marker"
sed -i 's/Unsigned invite-only beta packages; blocked until Phase 3 evidence/Signed desktop packages/' \
  "$desktop_marker/documentation/product/beta-feature-status.md"
assert_rejected "desktop-marker" "desktop" "$desktop_marker"

https_marker="$fixture_root/https-marker"
copy_fixture "$https_marker"
sed -i 's/Platform-trusted HTTPS only/Operator HTTPS only/' \
  "$https_marker/documentation/product/beta-feature-status.md"
assert_rejected "https-marker" "https" "$https_marker"

browser_matrix="$fixture_root/browser-matrix"
copy_fixture "$browser_matrix"
sed -i 's/Chrome, Firefox, Edge, desktop Safari, iOS Safari, Android Chrome/current browsers/' \
  "$browser_matrix/documentation/product/beta-feature-status.md"
assert_rejected "browser-matrix" "browserMatrix" "$browser_matrix"

browser_contradiction="$fixture_root/browser-contradiction"
copy_fixture "$browser_contradiction"
printf '\nHosted-browser delivery is ready for shipping.\n' \
  >>"$browser_contradiction/documentation/product/beta-feature-status.md"
assert_rejected "browser-contradiction" "browserService" "$browser_contradiction"

honest_browser_negation="$fixture_root/honest-browser-negation"
copy_fixture "$honest_browser_negation"
printf '\nHosted-browser delivery is not ready for shipping.\n' \
  >>"$honest_browser_negation/documentation/product/beta-feature-status.md"
assert_accepted "$honest_browser_negation"

backup_contradiction="$fixture_root/backup-contradiction"
copy_fixture "$backup_contradiction"
printf '\nEncrypted backup and recovery are shipping.\n' \
  >>"$backup_contradiction/documentation/product/beta-feature-status.md"
assert_rejected "backup-contradiction" "backupStatus" "$backup_contradiction"

for mutation in contract-non-object contract-missing-key contract-wrong-type; do
  malformed_contract="$fixture_root/$mutation"
  copy_fixture "$malformed_contract"
  python3 - "$mutation" "$malformed_contract/documentation/product/beta-contract.json" <<'PY'
import json
import sys

mutation, path = sys.argv[1:]
if mutation == "contract-non-object":
    document = []
else:
    with open(path, encoding="utf-8") as source:
        document = json.load(source)
    if mutation == "contract-missing-key":
        del document["modules"]
    else:
        document["modules"] = "chat"
with open(path, "w", encoding="utf-8") as destination:
    json.dump(document, destination)
    destination.write("\n")
PY
  if [ "$mutation" = "contract-non-object" ]; then
    assert_rejected "$mutation" "contract" "$malformed_contract"
  else
    assert_rejected "$mutation" "modules" "$malformed_contract"
  fi
done

voicemail_control="$fixture_root/voicemail-control"
copy_fixture "$voicemail_control"
printf '\nmux.HandleFunc("GET /api/v1/voicemail", func(http.ResponseWriter, *http.Request) {})\n' \
  >>"$voicemail_control/backend/main.go"
assert_accepted "$voicemail_control"

for alias in voice notifications watch-party media-list; do
  alias_fixture="$fixture_root/traefik-$alias"
  copy_fixture "$alias_fixture"
  python3 - "$alias" "$alias_fixture/config/dynamic/routes.yaml" <<'PY'
import sys

alias, path = sys.argv[1:]
with open(path, encoding="utf-8") as source:
    text = source.read()
insertion = f"    {alias}:\n      rule: 'Host({alias})'\n      service: telos-core\n\n"
with open(path, "w", encoding="utf-8") as destination:
    destination.write(text.replace("\n  middlewares:", "\n" + insertion + "  middlewares:", 1))
PY
  assert_rejected "traefik-$alias" "removedFeatures" "$alias_fixture"
done

watch_parties_route="$fixture_root/watch-parties-route"
copy_fixture "$watch_parties_route"
printf '\nmux.HandleFunc("GET /api/v1/watch-parties", func(http.ResponseWriter, *http.Request) {})\n' \
  >>"$watch_parties_route/backend/main.go"
assert_rejected "watch-parties-route" "removedFeatures" "$watch_parties_route"

honest_browser_isnt_ready="$fixture_root/honest-browser-isnt-ready"
copy_fixture "$honest_browser_isnt_ready"
printf "\\nHosted-browser delivery isn't ready.\\n" \
  >>"$honest_browser_isnt_ready/documentation/product/beta-feature-status.md"
assert_accepted "$honest_browser_isnt_ready"

mixed_browser_claim="$fixture_root/mixed-browser-claim"
copy_fixture "$mixed_browser_claim"
printf '\\nHosted-browser delivery is not ready, but shipping.\\n' \
  >>"$mixed_browser_claim/documentation/product/beta-feature-status.md"
assert_rejected "mixed-browser-claim" "browserService" "$mixed_browser_claim"

honest_backup_not_yet_ready="$fixture_root/honest-backup-not-yet-ready"
copy_fixture "$honest_backup_not_yet_ready"
printf '\\nEncrypted backup recovery is not yet ready.\\n' \
  >>"$honest_backup_not_yet_ready/documentation/product/beta-feature-status.md"
assert_accepted "$honest_backup_not_yet_ready"

mixed_backup_claim="$fixture_root/mixed-backup-claim"
copy_fixture "$mixed_backup_claim"
printf '\\nEncrypted backup and recovery are not ready, but shipping.\\n' \
  >>"$mixed_backup_claim/documentation/product/beta-feature-status.md"
assert_rejected "mixed-backup-claim" "backupStatus" "$mixed_backup_claim"

for state in ready shipping; do
  client_state="$fixture_root/client-$state"
  copy_fixture "$client_state"
  realize_client "$client_state"
  sed -i "s/implemented-awaiting-evidence/$state/" \
    "$client_state/documentation/product/beta-feature-status.md"
  assert_rejected "client-$state" "browserService" "$client_state"
done

client_stale_unavailable="$fixture_root/client-stale-unavailable"
copy_fixture "$client_stale_unavailable"
realize_client "$client_stale_unavailable"
printf '\\nHosted-browser delivery remains unavailable.\\n' \
  >>"$client_stale_unavailable/documentation/product/beta-feature-status.md"
assert_rejected "client-stale-unavailable" "browserService" "$client_stale_unavailable"

client_blocked_state="$fixture_root/client-blocked-state"
copy_fixture "$client_blocked_state"
realize_client "$client_blocked_state"
sed -i 's/implemented-awaiting-evidence/Blocked until Phase 2/' \
  "$client_blocked_state/documentation/product/beta-feature-status.md"
assert_rejected "client-blocked-state" "browserService" "$client_blocked_state"
