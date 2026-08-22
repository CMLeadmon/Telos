#!/usr/bin/env bash
# Product-truth guard: structural beta-contract verification plus the existing
# prohibited-surface checks. --root exists only to run isolated mutation fixtures.
set -euo pipefail

usage() {
  echo "usage: $0 [--root PATH]" >&2
  exit 2
}

if [ "$#" -eq 0 ]; then
  ROOT="$(git -C "$(dirname "$0")/.." rev-parse --show-toplevel)"
elif [ "$#" -eq 2 ] && [ "$1" = "--root" ] && [ -n "$2" ] && [[ "$2" != --* ]]; then
  ROOT="$2"
else
  usage
fi

[ -d "$ROOT" ] || { echo "product-truth: --root is not a directory: $ROOT" >&2; exit 2; }

python3 - "$ROOT" <<'PY'
import json
import re
import sys
from pathlib import Path

root = Path(sys.argv[1]).resolve()
errors = []


def fail(field, message):
    errors.append((field, message))


def file(relative):
    path = (root / relative).resolve()
    try:
        path.relative_to(root)
    except ValueError:
        fail("root", f"{relative} resolves outside --root")
        return None
    if not path.is_file():
        fail("root", f"missing required input {relative}")
        return None
    return path


def text(relative):
    path = file(relative)
    return "" if path is None else path.read_text(encoding="utf-8", errors="replace")


def document(relative, field):
    source = file(relative)
    if source is None:
        return None
    try:
        return json.loads(source.read_text(encoding="utf-8"))
    except json.JSONDecodeError as error:
        fail(field, f"{relative} is invalid JSON: {error.msg}")
        return None


expected = {
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
        "capacity", "host-exposure", "controlled-egress",
    ],
}
contract = document("documentation/product/beta-contract.json", "contract")
if contract is not None:
    if not isinstance(contract, dict) or set(contract) != set(expected):
        fail("contract", "beta-contract.json must use exactly the approved closed keys")
    else:
        for key, value in expected.items():
            if contract[key] != value:
                fail(key, "does not match the approved beta contract")


def balanced_array(source, symbol):
    found = re.search(rf"\bconst\s+{re.escape(symbol)}\s*=\s*\[", source)
    if not found:
        return None
    start, depth, quote, escaped = source.find("[", found.start()), 0, None, False
    for position in range(start, len(source)):
        char = source[position]
        if quote:
            if escaped:
                escaped = False
            elif char == "\\":
                escaped = True
            elif char == quote:
                quote = None
        elif char in ("'", '"'):
            quote = char
        elif char == "[":
            depth += 1
        elif char == "]":
            depth -= 1
            if depth == 0:
                return source[start + 1:position]
    return None


def module_entries(source):
    body = balanced_array(source, "MODULES")
    if body is None:
        return None
    entries, depth, start, quote, escaped = [], 0, None, None, False
    for position, char in enumerate(body):
        if quote:
            if escaped:
                escaped = False
            elif char == "\\":
                escaped = True
            elif char == quote:
                quote = None
        elif char in ("'", '"'):
            quote = char
        elif char == "{":
            if depth == 0:
                start = position
            depth += 1
        elif char == "}":
            depth -= 1
            if depth == 0 and start is not None:
                entries.append(body[start:position + 1])
    return entries if depth == 0 else None


def property_value(entry, name):
    found = re.search(rf"\b{re.escape(name)}\s*:\s*(['\"])(.*?)\1", entry, re.S)
    return None if found is None else found.group(2)


entries = module_entries(text("frontend/src/components/AppShell.tsx"))
if entries is None:
    fail("modules", "AppShell.tsx must declare a parseable MODULES array")
else:
    hrefs, capabilities = [], []
    for entry in entries:
        href, capability = property_value(entry, "href"), property_value(entry, "capability")
        if href is None or capability is None:
            fail("modules", "every MODULES entry needs href and capability")
        else:
            hrefs.append(href.removeprefix("/"))
            capabilities.append(capability)
    if contract is not None and hrefs != contract["modules"]:
        fail("modules", f"MODULES hrefs are {hrefs!r}, not the contract inventory")
    if capabilities != ["view_channel", "view_media", "view_library", "view_files"]:
        fail("modules", f"MODULES capabilities are {capabilities!r}, not canonical capabilities")


routes = re.findall(r'mux\.Handle\(\s*"([^"]+)"', text("backend/main.go"))
package = document("frontend/package.json", "removedFeatures")
dependencies = set()
if isinstance(package, dict):
    for section in ("dependencies", "devDependencies", "optionalDependencies", "peerDependencies"):
        if isinstance(package.get(section), dict):
            dependencies.update(package[section])
go_modules = set(re.findall(r"^\s*([A-Za-z0-9._/-]+)\s+v", text("backend/go.mod"), re.M))
if contract is not None:
    for feature in contract["removedFeatures"]:
        if any(f"/api/v1/{feature}" in route for route in routes):
            fail("removedFeatures", f"removed route prefix /api/v1/{feature} is present")
        token = feature.replace("-", "")
        if any(token in name.lower().replace("-", "") for name in dependencies | go_modules):
            fail("removedFeatures", f"removed dependency {feature} is present")


compose = text("docker-compose.yml")
services_part = re.search(r"^services:\s*$([\s\S]*)", compose, re.M)
compose_services = set() if services_part is None else set(
    re.findall(r"^ {2}([A-Za-z0-9][A-Za-z0-9_-]*):\s*$", services_part.group(1), re.M)
)
if contract is not None:
    if contract["apiService"] not in compose_services:
        fail("apiService", f"Compose lacks {contract['apiService']}")
    for feature in contract["removedFeatures"]:
        if feature in compose_services:
            fail("removedFeatures", f"removed Compose service {feature} is present")


traefik = text("config/dynamic/routes.yaml")
router_blocks = re.findall(
    r"^ {4}([A-Za-z0-9][A-Za-z0-9_-]*):\s*$([\s\S]*?)(?=^ {4}[A-Za-z0-9][A-Za-z0-9_-]*:\s*$|\Z)",
    traefik,
    re.M,
)
catch_all = []
for _, block in router_blocks:
    if re.search(r"PathPrefix\([^A-Za-z0-9]*/[^A-Za-z0-9]*\)", block):
        target = re.search(r"^ {6}service:\s*([A-Za-z0-9][A-Za-z0-9_-]*)\s*$", block, re.M)
        if target:
            catch_all.append(target.group(1))
traefik_section = re.search(r"^ {2}services:\s*$([\s\S]*)", traefik, re.M)
traefik_services = set() if traefik_section is None else set(
    re.findall(r"^ {4}([A-Za-z0-9][A-Za-z0-9_-]*):\s*$", traefik_section.group(1), re.M)
)
status = text("documentation/product/beta-feature-status.md")
if contract is not None:
    browser, api = contract["browserService"], contract["apiService"]
    browser_block = "Hosted-browser delivery is blocked until Phase 2."
    if api not in traefik_services:
        fail("apiService", f"Traefik service {api} is absent")
    if browser in compose_services:
        if catch_all != [browser] or browser not in traefik_services:
            fail("browserService", f"Traefik must route the catch-all to {browser}")
        if browser_block in status:
            fail("browserService", "browser delivery remains marked blocked after telos-client exists")
    else:
        if catch_all != [api]:
            fail("browserService", f"transitional catch-all must route to {api}")
        if browser_block not in status or re.search(r"hosted-browser delivery[^.\n]*\b(shipping|ready)\b", status, re.I):
            fail("browserService", "missing truthful blocked hosted-browser delivery marker")
    for platform in [contract["server"]["os"], contract["server"]["arch"],
                     contract["server"]["podmanMinimum"], *contract["desktop"]]:
        if platform not in status:
            fail("platform", f"beta status does not name {platform}")
    for surface in [*contract["modules"], contract["settingsSurface"]]:
        if surface.casefold() not in status.casefold():
            fail("modules", f"beta status does not name {surface}")
    backup_block = "Encrypted off-node backup and recovery are blocked until Phase 4 evidence exists."
    if backup_block not in status or re.search(r"encrypted off-node backup(?:s)?[^.\n]*\bshipping\b", status, re.I):
        fail("backupStatus", "blocked backup/recovery is missing or called shipping")


catalog = document("ci/phase-gates.json", "requiredExternalGates")
if contract is not None and isinstance(catalog, dict):
    gate_ids = [gate.get("id") for gate in catalog.get("gates", []) if isinstance(gate, dict)]
    for gate in contract["requiredExternalGates"]:
        if gate_ids.count(gate) != 1:
            fail("requiredExternalGates", f"gate {gate} must exist exactly once")


def shipped_files(relative):
    directory = (root / relative).resolve()
    if not directory.is_dir():
        return []
    try:
        directory.relative_to(root)
    except ValueError:
        fail("root", f"{relative} resolves outside --root")
        return []
    files = []
    for path in directory.rglob("*"):
        if not path.is_file():
            continue
        try:
            path.resolve().relative_to(root)
        except ValueError:
            fail("root", f"{path.relative_to(root)} resolves outside --root")
            continue
        files.append(path)
    return files


def scan(field, pattern, paths, skip=lambda path: False, reject_line=lambda line: False):
    expression, hits = re.compile(pattern, re.I), []
    for path in paths:
        for number, line in enumerate(path.read_text(encoding="utf-8", errors="replace").splitlines(), 1):
            if expression.search(line) and not skip(path) and not reject_line(line):
                hits.append(f"{path.relative_to(root)}:{number}:{line}")
    if hits:
        fail(field, "\n".join(hits))


frontend, backend = shipped_files("frontend/src"), shipped_files("backend")
source = frontend + backend
test_or_spec = lambda path: path.name.endswith("_test.go") or path.name.endswith(".spec.ts")
test_file = lambda path: test_or_spec(path) or path.name.endswith(".test.ts") or path.name.endswith(".test.tsx")
readme = [candidate for candidate in [file("README.md")] if candidate]
manifests = [candidate for candidate in [
    file("frontend/package.json"), file("frontend/package-lock.json"),
    file("backend/go.mod"), file("backend/go.sum"),
] if candidate]
scan("Oracle/AI control or surface", r"oracle|isOracle|ai[ -]?oracle", source, test_or_spec)
scan("AI API/runtime dependency", r"openai|@anthropic|anthropic-sdk|langchain|cohere|replicate|generative-ai|huggingface|ollama|@google/generative", manifests)
scan("AI product claim", r"AI[ -]powered|powered by AI|AI (summar|assistant|feature|module|oracle)|smart summar|AI-generated", frontend + readme, reject_line=lambda line: bool(re.search(r"no |without|removed|not (an?|our)", line, re.I)))
scan("OIDC-as-shipped claim", r"sign ?in with oidc|oidc login|oidc[- ]enabled|oidc federation (is|now) (available|supported|enabled)", frontend + readme)
license = file("LICENSE")
if license and not re.search(r"apache license", license.read_text(encoding="utf-8", errors="replace"), re.I):
    fail("license", "LICENSE does not carry the Apache License text")
scan("non-Apache Telos license claim", r"telos is (mit|gpl|bsd|proprietary)|licensed under (the )?(mit|gpl|bsd)", readme)
scan("absolute local-only privacy claim", r"nothing (ever )?leaves|never leaves (your|the) (node|device|server)|fully offline|100% private|completely private|no data ever leaves|air-?gapped", frontend + readme, reject_line=lambda line: bool(re.search(r"no |not |does not|without|never claims", line, re.I)))
scan("inert advertised control", r'aria-label="oracle"', frontend)
scan("removed feature surface reappeared", r"livekit|watch.?party|voice.?dock|useVoiceSession|useWatchParty|useNotificationStore|useMyListStore|/media-list", frontend + [path for path in backend if path.suffix == ".go"], test_file)

if errors:
    for field, message in errors:
        print(f"product-truth: VIOLATION ({field}):", file=sys.stderr)
        for line in message.splitlines():
            print(f"  {line}", file=sys.stderr)
    raise SystemExit(1)
print("product-truth: OK — contract and prohibited-surface inventories agree.")
PY
