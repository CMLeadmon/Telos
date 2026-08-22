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
contract_valid = False
if not isinstance(contract, dict):
    fail("contract", "beta-contract.json must be an object")
else:
    actual_keys = set(contract)
    missing_keys = set(expected) - actual_keys
    extra_keys = actual_keys - set(expected)
    for key in sorted(missing_keys):
        fail(key, "is missing from beta-contract.json")
    if extra_keys:
        fail("contract", f"has unknown keys: {sorted(extra_keys)!r}")
    for key, value in expected.items():
        if key not in contract:
            continue
        if type(contract[key]) is not type(value):
            fail(key, "has the wrong JSON type")
        elif contract[key] != value:
            fail(key, "does not match the approved beta contract")
    contract_valid = not errors


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
    if contract_valid and hrefs != contract["modules"]:
        fail("modules", f"MODULES hrefs are {hrefs!r}, not the contract inventory")
    if capabilities != ["view_channel", "view_media", "view_library", "view_files"]:
        fail("modules", f"MODULES capabilities are {capabilities!r}, not canonical capabilities")
    if contract_valid and not re.search(
        rf"<Link\s+[^>]*href=\{{?['\"]/{re.escape(contract['settingsSurface'])}['\"]\}}?",
        text("frontend/src/components/AppShell.tsx"),
        re.S,
    ):
        fail("settingsSurface", "AppShell.tsx has no canonical settings Link outside MODULES")


routes = re.findall(r'mux\.Handle(?:Func)?\(\s*"([^"]+)"', text("backend/main.go"))
package = document("frontend/package.json", "removedFeatures")
dependencies = set()
if isinstance(package, dict):
    for section in ("dependencies", "devDependencies", "optionalDependencies", "peerDependencies"):
        if isinstance(package.get(section), dict):
            dependencies.update(package[section])
go_modules = set(re.findall(r"^\s*([A-Za-z0-9._/-]+)\s+v", text("backend/go.mod"), re.M))
removed_route_families = {
    "livekit": (r"/livekit(?:/|$)",),
    "voice-rooms": (r"/api/v1/voice(?:/|$)",),
    "watch-parties": (
        r"/api/v1/watch-party(?:/|$)",
        r"/api/v1/watch_parties(?:/|$)",
        r"/api/v1/watch_party(?:/|$)",
        r"/api/v1/watch-parties(?:/|$)",
    ),
    "notification-inbox": (r"/api/v1/notifications(?:/|$)",),
    "my-list": (
        r"/api/v1/users/me/media-list(?:/|$)",
        r"/api/v1/my-list(?:/|$)",
        r"/api/v1/media-list(?:/|$)",
    ),
}
if contract_valid:
    for feature in contract["removedFeatures"]:
        if any(any(re.search(pattern, route) for pattern in removed_route_families[feature]) for route in routes):
            fail("removedFeatures", f"removed route family {feature} is present")
        token = feature.replace("-", "")
        if any(token in name.lower().replace("-", "") for name in dependencies | go_modules):
            fail("removedFeatures", f"removed dependency {feature} is present")


compose = text("docker-compose.yml")
services_part = re.search(r"^services:\s*$([\s\S]*)", compose, re.M)
compose_services = set() if services_part is None else set(
    re.findall(r"^ {2}([A-Za-z0-9][A-Za-z0-9_-]*):\s*$", services_part.group(1), re.M)
)
if contract_valid:
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
router_records = []
for name, block in router_blocks:
    target = re.search(r"^ {6}service:\s*([A-Za-z0-9][A-Za-z0-9_-]*)\s*$", block, re.M)
    priority = re.search(r"^ {6}priority:\s*(\d+)\s*$", block, re.M)
    router_records.append(
        {
            "name": name,
            "rule": block,
            "target": None if target is None else target.group(1),
            "priority": None if priority is None else int(priority.group(1)),
        }
    )
catch_all = [
    record for record in router_records
    if re.search(r"PathPrefix\([^A-Za-z0-9]*/[^A-Za-z0-9]*\)", record["rule"])
]
api_routers = [record for record in router_records if "/api/v1" in record["rule"]]
generic_api = [
    record for record in router_records
    if re.search(r"PathPrefix\([^A-Za-z0-9]*/api/v1[^A-Za-z0-9]*\)", record["rule"])
]
traefik_section = re.search(r"^ {2}services:\s*$([\s\S]*)", traefik, re.M)
traefik_services = set() if traefik_section is None else set(
    re.findall(r"^ {4}([A-Za-z0-9][A-Za-z0-9_-]*):\s*$", traefik_section.group(1), re.M)
)
traefik_urls = {} if traefik_section is None else {
    name: re.findall(r"^ {10}- url:\s*(\S+)\s*$", block, re.M)
    for name, block in re.findall(
        r"^ {4}([A-Za-z0-9][A-Za-z0-9_-]*):\s*$([\s\S]*?)(?=^ {4}[A-Za-z0-9][A-Za-z0-9_-]*:\s*$|\Z)",
        traefik_section.group(1),
        re.M,
    )
}
status = text("documentation/product/beta-feature-status.md")
status_rows = {
    label.strip(): value.strip()
    for label, value in re.findall(r"^\|\s*([^|]+?)\s*\|\s*([^|]+?)\s*\|\s*$", status, re.M)
    if label.strip() != "Delivery boundary"
}
if contract_valid:
    browser, api = contract["browserService"], contract["apiService"]
    browser_block = "Hosted-browser delivery is blocked until Phase 2."
    if api not in traefik_services:
        fail("apiService", f"Traefik service {api} is absent")
    elif traefik_urls.get(api) != [f"http://{api}:8080"]:
        fail("apiService", f"Traefik service {api} must resolve to http://{api}:8080")
    if any(record["target"] != api for record in api_routers):
        fail("apiService", "every API-matching Traefik router must target the API service")
    removed_traefik_aliases = {
        "livekit": {"livekit"},
        "voice-rooms": {"voice", "voice-rooms", "voice_rooms"},
        "watch-parties": {"watch-party", "watch_party", "watch-parties", "watch_parties"},
        "notification-inbox": {"notifications", "notification-inbox", "notification_inbox"},
        "my-list": {"media-list", "media_list", "my-list", "my_list"},
    }
    for feature in contract["removedFeatures"]:
        traefik_names = set(router["name"] for router in router_records) | traefik_services
        if traefik_names & removed_traefik_aliases[feature]:
            fail("removedFeatures", f"removed Traefik router or service {feature} is present")
    if browser in compose_services:
        if len(catch_all) != 1 or catch_all[0]["target"] != browser or browser not in traefik_services:
            fail("browserService", f"Traefik must route the catch-all to {browser}")
        elif traefik_urls.get(browser) != [f"http://{browser}:8080"]:
            fail("browserService", f"Traefik service {browser} must resolve to http://{browser}:8080")
        if (
            status_rows.get("Hosted-browser delivery") != "implemented-awaiting-evidence"
            or browser_block in status
            or re.search(r"hosted-browser delivery.{0,100}\bunavailable\b", status, re.I | re.S)
        ):
            fail("browserService", "browser delivery lacks the truthful post-transition state")
        if (
            len(generic_api) != 1
            or generic_api[0]["target"] != api
            or generic_api[0]["priority"] is None
            or catch_all[0]["priority"] is None
            or generic_api[0]["priority"] <= catch_all[0]["priority"]
            or f"PathPrefix({chr(96)}/api/v1{chr(96)})" not in generic_api[0]["rule"]
            or f"Host({chr(96)}" not in generic_api[0]["rule"]
        ):
            fail("apiService", "realized client topology needs a higher-priority generic /api/v1 fallback")
    else:
        if len(catch_all) != 1 or catch_all[0]["target"] != api:
            fail("browserService", f"transitional catch-all must route to {api}")
        if (
            browser_block not in status
            or status_rows.get("Hosted-browser delivery") != "Blocked until Phase 2"
        ):
            fail("browserService", "missing truthful blocked hosted-browser delivery marker")
    def positive_claim(subject):
        for line in status.splitlines():
            if not re.search(subject, line, re.I):
                continue
            for claim in re.finditer(r"\b(shipping|ready)\b", line, re.I):
                prefix = line[max(0, claim.start() - 32):claim.start()]
                if not re.search(
                    r"\b(?:not(?:\s+yet)?|never|without|isn't)\s+$"
                    r"|\b(?:not(?:\s+yet)?|isn't)\s+ready\s+for\s+$",
                    prefix,
                    re.I,
                ):
                    return True
        return False

    if positive_claim(r"hosted[\s-]browser delivery"):
        fail("browserService", "hosted-browser delivery is called ready or shipping")
    for platform in [contract["server"]["os"], contract["server"]["arch"],
                     contract["server"]["podmanMinimum"], *contract["desktop"]]:
        if platform not in status:
            fail("platform", f"beta status does not name {platform}")
    for surface in [*contract["modules"], contract["settingsSurface"]]:
        if surface.casefold() not in status.casefold():
            fail("modules", f"beta status does not name {surface}")
    backup_block = "Encrypted off-node backup and recovery are blocked until Phase 4 evidence exists."
    required_status_rows = {
        "desktop": ("Desktop distribution", "Unsigned invite-only beta packages; blocked until Phase 3 evidence"),
        "https": ("HTTPS trust", "Platform-trusted HTTPS only"),
        "browserMatrix": ("Browser matrix", "Chrome, Firefox, Edge, desktop Safari, iOS Safari, Android Chrome"),
        "backupStatus": ("Backup/recovery", "Blocked until Phase 4 evidence"),
    }
    for field, (label, value) in required_status_rows.items():
        if status_rows.get(label) != value:
            fail(field, f"beta status is missing required {label} table value")
    if backup_block not in status or positive_claim(r"(?:encrypted )?(backup|recovery)"):
        fail("backupStatus", "blocked backup/recovery is missing or called shipping")


catalog = document("ci/phase-gates.json", "requiredExternalGates")
if contract_valid and isinstance(catalog, dict):
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
        try:
            path.resolve().relative_to(root)
        except ValueError:
            fail("root", f"{path.relative_to(root)} resolves outside --root")
            continue
        if not path.is_file():
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
