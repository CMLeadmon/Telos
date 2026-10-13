#!/usr/bin/env bash
# validate-compose.sh — render and assert invariants of the production Compose
# model without running it or printing any secret value.
#
# Usage:
#   scripts/validate-compose.sh --env-file <path> [--checks a,b,c] [--root <dir>]
#
# Known checks (default: all):
#   core-ingress       Only Traefik publishes HTTP entry points (80/443).
#   stop-grace         telos-core.stop_grace_period is at least 75 seconds.
#   database-observer  pg_stat_statements is preloaded and no app carries the observer URL.
#   standalone-client  telos-client is isolated: ingress-only, no ports/volumes/secrets, hardened.
#   route-split        Traefik sends /api/v1/* to telos-core and every other path to telos-client.
#   setup-overlay      docker-compose.setup.yml publishes only the setup relay's two ports, on 127.0.0.1.
#
# --root points the checks at another checkout (used only by the mutation tests).
# A missing podman or Compose provider exits 3 (not_run): nothing was rendered.
#
# The rendered config is written to a mode-0600 temp file and removed on exit;
# only check names and statuses are printed.
set -euo pipefail

env_file=""
root=""
known_checks="core-ingress stop-grace database-observer standalone-client route-split setup-overlay"
checks="core-ingress,stop-grace,database-observer,standalone-client,route-split,setup-overlay"
while [ $# -gt 0 ]; do
	case "$1" in
	--root)
		shift
		root="${1:-}"
		;;
	--env-file)
		shift
		env_file="$1"
		;;
	--checks)
		shift
		checks="$1"
		;;
	*)
		echo "unknown argument: $1" >&2
		exit 2
		;;
	esac
	shift
done

if [ -n "$root" ] && [ ! -d "$root" ]; then
	echo "validate-compose: --root must be a directory" >&2
	exit 2
fi
cd "${root:-$(git rev-parse --show-toplevel)}"

for requested in $(printf '%s' "$checks" | tr ',' ' '); do
	case " $known_checks " in
	*" $requested "*) ;;
	*)
		echo "validate-compose: unknown check: $requested (known: $known_checks)" >&2
		exit 2
		;;
	esac
done

if [ -z "$env_file" ] || [ ! -f "$env_file" ]; then
	echo "validate-compose: --env-file must reference a readable file" >&2
	exit 2
fi

# A missing container runtime or Compose provider is an unavailable prerequisite
# (exit 3, not_run), never a failed model: nothing was rendered or judged.
if ! command -v podman >/dev/null 2>&1; then
	echo "not_run: podman is required to render the Compose model" >&2
	exit 3
fi
if ! podman compose version >/dev/null 2>&1; then
	echo "not_run: a podman compose provider (podman-compose) is required" >&2
	exit 3
fi

rendered="$(mktemp)"
chmod 0600 "$rendered"
cleanup() { rm -f "$rendered"; }
trap cleanup EXIT

if ! podman compose --env-file "$env_file" -f docker-compose.yml config >"$rendered" 2>/dev/null; then
	echo "validate-compose: compose model failed to render" >&2
	exit 1
fi

fail=0
report() { printf '%-14s %s\n' "$1" "$2"; }

want() { case ",$checks," in *",$1,"*) return 0 ;; *) return 1 ;; esac; }

if want core-ingress; then
	# Every published port must belong to Traefik (80/443); no other service
	# may publish an HTTP entry point.
	bad="$(python3 - "$rendered" <<'PY'
import sys, yaml
doc = yaml.safe_load(open(sys.argv[1]))
allowed = {
    ("traefik", "80"), ("traefik", "443"),
}
bad = []
for name, svc in (doc.get("services") or {}).items():
    for p in (svc.get("ports") or []):
        published = p.get("published") if isinstance(p, dict) else str(p).split(":")[0]
        target = str(published)
        if (name, target) not in allowed:
            bad.append(f"{name}:{target}")
print(",".join(bad))
PY
)"
	if [ -n "$bad" ]; then
		report core-ingress "FAIL (unexpected published ports)"
		fail=1
	else
		report core-ingress "OK"
	fi
fi

if want stop-grace; then
	secs="$(python3 - "$rendered" <<'PY'
import sys, yaml
doc = yaml.safe_load(open(sys.argv[1]))
svc = (doc.get("services") or {}).get("telos-core", {})
val = svc.get("stop_grace_period", "0s")
# Accept "75s" or nanosecond int forms.
s = str(val)
if s.endswith("s") and s[:-1].isdigit():
    print(int(s[:-1]))
elif s.isdigit():
    print(int(s) // 1_000_000_000)
else:
    print(0)
PY
)"
	if [ "${secs:-0}" -ge 75 ]; then
		report stop-grace "OK (${secs}s)"
	else
		report stop-grace "FAIL (${secs}s < 75s)"
		fail=1
	fi
fi

if want database-observer; then
	obs_ok=1
	# pg_stat_statements must be preloaded with parameter redaction.
	if ! grep -q 'shared_preload_libraries=pg_stat_statements' "$rendered"; then
		report database-observer "FAIL (pg_stat_statements not preloaded)"
		obs_ok=0
		fail=1
	fi
	# No application service may carry the observer credential.
	if grep -qiE 'DATABASE_OBSERVER_URL' "$rendered"; then
		report database-observer "FAIL (an application service has the observer URL)"
		obs_ok=0
		fail=1
	fi
	[ "$obs_ok" -eq 1 ] && report database-observer "OK"
fi

if want standalone-client; then
	client_problem="$(python3 - "$rendered" <<'PY'
import sys
import yaml

doc = yaml.safe_load(open(sys.argv[1]))
services = doc.get("services") or {}
problems = []


def names(value):
    if isinstance(value, dict):
        return set(value)
    return set(value or [])


def env_keys(service):
    env = service.get("environment") or {}
    if isinstance(env, dict):
        return set(env)
    return {str(item).split("=", 1)[0] for item in env}


client = services.get("telos-client")
if client is None:
    problems.append("telos-client service is missing")
else:
    build = client.get("build")
    context = build.get("context") if isinstance(build, dict) else build
    dockerfile = build.get("dockerfile") if isinstance(build, dict) else None
    if not context or str(context).rstrip("/").split("/")[-1] != "frontend" or dockerfile != "Dockerfile":
        problems.append("telos-client must build frontend/Dockerfile")
    if names(client.get("networks")) != {"telos-ingress"}:
        problems.append(f"telos-client networks must be exactly telos-ingress, found {sorted(names(client.get('networks')))}")
    if client.get("ports"):
        problems.append("telos-client must not publish host ports")
    if client.get("volumes"):
        problems.append("telos-client must not mount volumes")
    forbidden = ("PASSWORD", "TOKEN", "SECRET", "DATABASE", "REDIS", "STORAGE", "JELLYFIN", "GRIMMORY")
    for key in sorted(env_keys(client)):
        if any(word in key.upper() for word in forbidden):
            problems.append(f"telos-client environment carries secret-like variable {key}")
    health = client.get("healthcheck") or {}
    if "/healthz" not in " ".join(str(part) for part in health.get("test", [])):
        problems.append("telos-client needs a /healthz health check")
    if client.get("read_only") is not True:
        problems.append("telos-client must run with a read-only root filesystem")
    if [str(c).upper() for c in client.get("cap_drop") or []] != ["ALL"]:
        problems.append("telos-client must drop all capabilities")
    if not any("no-new-privileges" in str(item) for item in client.get("security_opt") or []):
        problems.append("telos-client must set no-new-privileges")
    if "depends_on" in client:
        problems.append("telos-client must not depend on other services")
    if client.get("secrets") or client.get("configs"):
        problems.append("telos-client must not receive secrets or configs")

core = services.get("telos-core") or {}
if names(core.get("networks")) != {"telos-ingress", "telos-backend", "telos-db"}:
    problems.append("telos-core must join exactly telos-ingress, telos-backend and telos-db")
if core.get("ports"):
    problems.append("telos-core must not publish host ports")
build = core.get("build")
arguments = (build.get("args") if isinstance(build, dict) else None) or {}
arguments = arguments if isinstance(arguments, dict) else dict(item.split("=", 1) for item in arguments)
if str(arguments.get("EMBED_FRONTEND", "false")).lower() == "true" or (isinstance(build, dict) and build.get("target") not in (None, "")):
    problems.append("telos-core must use the default headless target, never the embedded frontend")
for key in sorted(env_keys(core)):
    if key == "EMBED_FRONTEND":
        problems.append("telos-core must not set EMBED_FRONTEND")
print("; ".join(problems))
PY
)"
	if [ -n "$client_problem" ]; then
		report standalone-client "FAIL ($client_problem)"
		fail=1
	else
		report standalone-client "OK"
	fi
fi

if want route-split; then
	route_problem="$(python3 - "config/dynamic/routes.yaml" <<'PY'
import re
import sys
import yaml

DOMAIN = "node.example"
routes = yaml.safe_load(open(sys.argv[1]))["http"]
routers, services = routes["routers"], routes["services"]
problems = []


def tokens(rule):
    pattern = re.compile(r"\s*(\(|\)|&&|\|\||[A-Za-z]+\(`[^`]*`(?:\s*,\s*`[^`]*`)*\))")
    position, out = 0, []
    rule = rule.strip()
    while position < len(rule):
        match = pattern.match(rule, position)
        if not match:
            raise ValueError(f"cannot parse rule near {rule[position:position + 20]!r}")
        out.append(match.group(1))
        position = match.end()
    return out


def evaluate(rule, request):
    items = tokens(rule.replace('{{ env "TELOS_DOMAIN" }}', DOMAIN))
    index = 0

    def peek():
        return items[index] if index < len(items) else None

    def take():
        nonlocal index
        index += 1
        return items[index - 1]

    def matcher(text):
        name, arguments = text.split("(", 1)
        values = re.findall(r"`([^`]*)`", arguments)
        if name == "Host":
            return any(value == request["host"] for value in values)
        if name == "Path":
            return any(value == request["path"] for value in values)
        if name == "PathPrefix":
            return any(request["path"].startswith(value) for value in values)
        if name == "PathRegexp":
            return any(re.search(value, request["path"]) for value in values)
        raise ValueError(f"unsupported matcher {name}")

    def factor():
        if peek() == "(":
            take()
            value = disjunction()
            if take() != ")":
                raise ValueError("unbalanced parentheses")
            return value
        return matcher(take())

    def conjunction():
        value = factor()
        while peek() == "&&":
            take()
            value = factor() and value
        return value

    def disjunction():
        value = conjunction()
        while peek() == "||":
            take()
            value = conjunction() or value
        return value

    return disjunction()


def route(path):
    request = {"host": DOMAIN, "path": path}
    matches = [(spec.get("priority"), name, spec) for name, spec in routers.items() if evaluate(spec["rule"], request)]
    if not matches:
        return None, "no router matches"
    matches.sort(key=lambda item: -(item[0] or 0))
    if len(matches) > 1 and matches[0][0] == matches[1][0]:
        return None, f"routers {matches[0][1]} and {matches[1][1]} tie at priority {matches[0][0]}"
    return matches[0][2]["service"], matches[0][1]


API_PATHS = [
    "/api/v1/auth/login", "/api/v1/auth/me", "/api/v1/health", "/api/v1/health/ready", "/api/v1/chat/ws",
    "/api/v1/events/ws", "/api/v1/stream/video/abc/playback-locator", "/api/v1/media/items", "/api/v1/files/1",
    "/api/v1/library/books/1/cover", "/api/v1/users/me/avatar", "/api/v1/not-a-route", "/api/v1/",
]
WEB_PATHS = ["/", "/chat/", "/login/", "/connect/", "/_next/static/chunks/a.js", "/favicon.ico", "/logos/telos.svg"]
try:
    for path in API_PATHS:
        service, detail = route(path)
        if service != "telos-core":
            problems.append(f"{path} routes to {service or 'nothing'} ({detail}), not telos-core")
    for path in WEB_PATHS:
        service, detail = route(path)
        if service != "telos-client":
            problems.append(f"{path} routes to {service or 'nothing'} ({detail}), not telos-client")
    for name, spec in routers.items():
        if "/api/" in spec["rule"] and spec["service"] != "telos-core":
            problems.append(f"API router {name} targets {spec['service']}")
    for name in ("telos-core", "telos-client"):
        urls = [server.get("url") for server in (services.get(name, {}).get("loadBalancer", {}).get("servers") or [])]
        if urls != [f"http://{name}:8080"]:
            problems.append(f"service {name} must resolve to http://{name}:8080, found {urls}")
    catch_all = [n for n, s in routers.items() if "PathPrefix(`/`)" in s["rule"]]
    if len(catch_all) != 1 or routers[catch_all[0]]["service"] != "telos-client":
        problems.append("exactly one catch-all router must target telos-client")
    elif routers[catch_all[0]].get("priority") != 1:
        problems.append("the browser catch-all must have the lowest priority 1")
except ValueError as error:
    problems.append(str(error))
print("; ".join(problems))
PY
)"
	if [ -n "$route_problem" ]; then
		report route-split "FAIL ($route_problem)"
		fail=1
	else
		report route-split "OK"
	fi
fi

if want setup-overlay; then
	overlay_problem="$(python3 - "docker-compose.setup.yml" "$rendered" <<'PY'
import re
import sys
import yaml

problems = []
try:
    overlay = yaml.safe_load(open(sys.argv[1]))
except OSError:
    print("docker-compose.setup.yml is missing")
    raise SystemExit
services = (overlay or {}).get("services") or {}
if set(overlay or {}) != {"networks", "services"}:
    problems.append("the overlay may only define networks and services")
if set(services) != {"telos-setup-proxy"}:
    problems.append(f"the overlay may only add telos-setup-proxy, found {sorted(services)}")
# The relay is the ONLY thing that publishes, and only to 127.0.0.1, one fixed port each.
proxy = services.get("telos-setup-proxy") or {}
ports = [str(p) for p in proxy.get("ports") or []]
expected = [r"127\.0\.0\.1:\$\{JELLYFIN_SETUP_PORT:-8096\}:8096", r"127\.0\.0\.1:\$\{GRIMMORY_SETUP_PORT:-6060\}:6060"]
if len(ports) != 2 or not all(re.fullmatch(pattern, port) for pattern, port in zip(expected, ports)):
    problems.append(f"the relay must publish exactly the two loopback setup ports, found {ports}")
networks = set(proxy.get("networks") or [])
if networks != {"telos-backend", "telos-setup"}:
    problems.append(f"the relay must join exactly telos-backend and telos-setup, found {sorted(networks)}")
if (overlay.get("networks") or {}).get("telos-setup", {}).get("internal"):
    problems.append("telos-setup must be an ordinary (non-internal) network: that is what makes the loopback port work")
if proxy.get("user") != "10001:10001" or not proxy.get("read_only") or "ALL" not in (proxy.get("cap_drop") or []):
    problems.append("the relay must run as 10001:10001, read-only, with all capabilities dropped")
command = [str(c) for c in proxy.get("command") or []]
if command[:2] != ["./telos-core", "setup-proxy"]:
    problems.append("the relay must run ./telos-core setup-proxy")
# The production model itself must stay port-free for both services.
rendered = yaml.safe_load(open(sys.argv[2]))
for name in ("jellyfin", "grimmory", "telos-setup-proxy"):
    if (rendered.get("services", {}).get(name) or {}).get("ports"):
        problems.append(f"the production model publishes a port for {name}")
print("; ".join(problems))
PY
)"
	if [ -n "$overlay_problem" ]; then
		report setup-overlay "FAIL ($overlay_problem)"
		fail=1
	else
		report setup-overlay "OK"
	fi
fi

exit "$fail"
