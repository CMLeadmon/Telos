#!/usr/bin/env bash
# verify-clean-checkout.sh — prove a Telos checkout contains every release
# input required to build and boot.
#
# Usage:
#   scripts/verify-clean-checkout.sh --inventory-only [--source-ref <tree-ish>]
#   scripts/verify-clean-checkout.sh --source-ref <tree-ish> --evidence-out <absolute-path>
#   scripts/verify-clean-checkout.sh --list-required
#
# Exit codes:
#   0  all requested checks passed
#   2  usage error
#   10 tracked/input inventory failed
#   20 build or test failed
#   30 compose boot/readiness failed
#   40 teardown or artifact-integrity failed
#
# Inventory mode checks the current index and working tree by default; it
# never silently substitutes a previous HEAD. Full mode refuses to run
# without --source-ref: it exports that exact tree-ish, generates a
# noncommittable random test environment, validates Compose, builds from the
# export, boots an isolated stack, runs smoke probes, writes sanitized
# evidence outside the repository, and tears everything down.
#
# TELOS_VERIFY_FAULT=readiness|teardown is harness-only fault injection used
# by scripts/tests/verify-clean-checkout-test.sh to prove exit codes 30/40.
set -euo pipefail

EXIT_OK=0
EXIT_USAGE=2
EXIT_INVENTORY=10
EXIT_BUILD=20
EXIT_BOOT=30
EXIT_TEARDOWN=40

MODE=""
SOURCE_REF=""
EVIDENCE_OUT=""

usage() {
	sed -n '2,18p' "$0" >&2
	exit "$EXIT_USAGE"
}

while [ $# -gt 0 ]; do
	case "$1" in
	--inventory-only) MODE="inventory" ;;
	--list-required) MODE="list-required" ;;
	--source-ref)
		shift
		[ $# -gt 0 ] || usage
		SOURCE_REF="$1"
		;;
	--evidence-out)
		shift
		[ $# -gt 0 ] || usage
		EVIDENCE_OUT="$1"
		;;
	*) usage ;;
	esac
	shift
done

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

# Runtime-critical release inputs. One literal path per line. Migrations are
# validated separately for contiguity.
required_paths() {
	cat <<'EOF'
.env.example
.nvmrc
AGENTS.md
docker-compose.yml
docker-compose.dev.yml
config/traefik.yaml
config/dynamic/routes.yaml
backend/go.mod
backend/go.sum
backend/Dockerfile
backend/main.go
frontend/package.json
frontend/package-lock.json
frontend/next.config.ts
documentation/operations/backup-and-restore.md
documentation/operations/repository-baseline.md
scripts/backup.sh
scripts/restore.sh
scripts/verify-clean-checkout.sh
scripts/tests/verify-clean-checkout-test.sh
EOF
}

if [ "$MODE" = "list-required" ]; then
	required_paths
	exit "$EXIT_OK"
fi

[ -n "$MODE" ] || [ -n "$SOURCE_REF" ] || usage

fail_inventory() {
	echo "inventory: $1" >&2
	INVENTORY_FAILED=1
}

path_present() {
	if [ -n "$SOURCE_REF" ]; then
		git cat-file -e "$SOURCE_REF:$1" 2>/dev/null
	else
		git ls-files --error-unmatch "$1" >/dev/null 2>&1
	fi
}

list_tree_paths() {
	if [ -n "$SOURCE_REF" ]; then
		git ls-tree -r --name-only "$SOURCE_REF" -- "$1"
	else
		git ls-files -- "$1"
	fi
}

tree_file() {
	if [ -n "$SOURCE_REF" ]; then
		git show "$SOURCE_REF:$1"
	else
		cat "$1"
	fi
}

run_inventory() {
	INVENTORY_FAILED=0

	while IFS= read -r p; do
		path_present "$p" || fail_inventory "required release input is not tracked: $p"
	done < <(required_paths)

	# Migrations must be numeric, ordered, and contiguous from 0001.
	local versions expected=1 count=0
	versions="$(list_tree_paths backend/db/migrations | sed -n 's|.*/\([0-9]\{4\}\)_.*\.sql$|\1|p' | sort)"
	if [ -z "$versions" ]; then
		fail_inventory "no tracked migrations under backend/db/migrations"
	else
		while IFS= read -r v; do
			if [ "$((10#$v))" -ne "$expected" ]; then
				fail_inventory "migration versions are not contiguous: expected $(printf '%04d' "$expected"), found $v"
				expected=$((10#$v))
			fi
			expected=$((expected + 1))
			count=$((count + 1))
		done <<<"$versions"
	fi

	# No untracked runtime-critical inputs (working-tree mode only).
	if [ -z "$SOURCE_REF" ]; then
		local untracked
		untracked="$(git ls-files --others --exclude-standard -- \
			backend/db/migrations config scripts \
			docker-compose.yml docker-compose.dev.yml \
			documentation/operations)"
		if [ -n "$untracked" ]; then
			while IFS= read -r p; do
				fail_inventory "runtime-critical input is untracked: $p"
			done <<<"$untracked"
		fi
	fi

	# Production compose: only Traefik may publish HTTP entry points; no other
	# service may publish any port.
	local port_report
	port_report="$(tree_file docker-compose.yml | awk '
		/^services:/ { in_services=1; next }
		in_services && /^[a-zA-Z0-9_-]+:/ { in_services=0 }
		/^  [a-zA-Z0-9_-]+:/ { service=$1; sub(":", "", service); in_ports=0 }
		/^    ports:/ { in_ports=1; next }
		in_ports && /^      - / {
			port=$2; gsub(/"/, "", port);
			print service " " port; next
		}
		in_ports && !/^      / { in_ports=0 }
	')"
	while IFS=' ' read -r service port; do
		[ -n "$service" ] || continue
		case "$service:$port" in
		traefik:80:80 | traefik:443:443 | \
			traefik:\${TRAEFIK_HTTP_PORT:-80}:80 | \
			traefik:\${TRAEFIK_HTTPS_PORT:-443}:443) ;;
		*) fail_inventory "unreviewed published port in production compose: service=$service port=$port" ;;
		esac
	done <<<"$port_report"

	# Development overlay bindings must be loopback-only.
	if path_present docker-compose.dev.yml; then
		local dev_ports
		dev_ports="$(tree_file docker-compose.dev.yml | grep -E '^\s+- "' | tr -d ' "-' || true)"
		while IFS= read -r p; do
			[ -n "$p" ] || continue
			case "$p" in
			127.0.0.1:*) ;;
			*) fail_inventory "development overlay binding is not loopback-only: $p" ;;
			esac
		done <<<"$dev_ports"
	fi

	# Pinned-toolchain assertions: no build input may float.
	tree_file backend/go.mod | grep -qE '^go 1\.26\.5$' ||
		fail_inventory "backend/go.mod does not pin go 1.26.5"
	[ "$(tree_file .nvmrc 2>/dev/null || true)" = "24.18.0" ] ||
		fail_inventory ".nvmrc does not pin Node.js 24.18.0"
	tree_file backend/Dockerfile | grep -q 'FROM node:24\.18\.0-alpine' ||
		fail_inventory "backend/Dockerfile does not pin node:24.18.0-alpine"
	tree_file backend/Dockerfile | grep -q 'FROM golang:1\.26\.5-alpine' ||
		fail_inventory "backend/Dockerfile does not pin golang:1.26.5-alpine"
	tree_file docker-compose.yml | grep -q 'image: postgres:16\.14-alpine' ||
		fail_inventory "docker-compose.yml does not pin postgres:16.14-alpine"
	tree_file frontend/package.json | grep -q '"next": "16\.2\.10"' ||
		fail_inventory "frontend/package.json does not pin next 16.2.10 exactly"
	tree_file frontend/package.json | grep -q '"postcss": "8\.5\.10"' ||
		fail_inventory "frontend/package.json does not pin the postcss 8.5.10 override"
	tree_file frontend/package.json | grep -q '"epubjs": "0\.4\.2"' ||
		fail_inventory "frontend/package.json does not pin epubjs 0.4.2"
	if tree_file frontend/package.json | grep -nE '"(dependencies|devDependencies)"' -A 40 |
		grep -E '": "[\^~]' >/dev/null; then
		fail_inventory "frontend/package.json contains floating (^/~) dependency ranges"
	fi

	if [ "$INVENTORY_FAILED" -ne 0 ]; then
		exit "$EXIT_INVENTORY"
	fi
	echo "inventory: all required release inputs present ($count migrations, contiguous)"
}

if [ "$MODE" = "inventory" ]; then
	run_inventory
	exit "$EXIT_OK"
fi

# ---------------------------------------------------------------------------
# Full mode: export, build, boot, probe, evidence, teardown.
# ---------------------------------------------------------------------------
[ -n "$SOURCE_REF" ] || usage
[ -n "$EVIDENCE_OUT" ] || usage
case "$EVIDENCE_OUT" in
/*) ;;
*)
	echo "evidence path must be absolute: $EVIDENCE_OUT" >&2
	exit "$EXIT_USAGE"
	;;
esac
case "$EVIDENCE_OUT" in
"$repo_root"/*)
	echo "evidence path must be outside the repository: $EVIDENCE_OUT" >&2
	exit "$EXIT_USAGE"
	;;
esac

git rev-parse --verify --quiet "${SOURCE_REF}^{tree}" >/dev/null || {
	echo "unknown --source-ref: $SOURCE_REF" >&2
	exit "$EXIT_USAGE"
}

FAULT="${TELOS_VERIFY_FAULT:-}"

run_inventory

rand() { od -An -N"${1:-24}" -tx1 /dev/urandom | tr -d ' \n'; }

proj="telosverify$(rand 3)"
port="$((20000 + $(od -An -N2 -tu2 /dev/urandom | tr -d ' ') % 9999))"
tmp="$(mktemp -d "/tmp/${proj}.XXXXXX")"
src="$tmp/src"
final_exit="$EXIT_OK"

teardown() {
	set +e
	podman compose -p "$proj" --env-file "$src/.env" \
		-f "$src/docker-compose.yml" -f "$tmp/smoke-overlay.yml" \
		down -v --timeout 10 >/dev/null 2>&1
	if podman ps -a --format '{{.Names}}' | grep -q "^${proj}-"; then
		echo "teardown: containers with prefix ${proj}- survived" >&2
		[ "$final_exit" -eq "$EXIT_OK" ] && final_exit="$EXIT_TEARDOWN"
		podman rm -f $(podman ps -a --format '{{.Names}}' | grep "^${proj}-") >/dev/null 2>&1
	fi
	podman network rm -f "${proj}-ingress" "${proj}-backend" "${proj}-db" >/dev/null 2>&1
	podman volume ls --format '{{.Name}}' | grep "^${proj}_" | xargs -r podman volume rm -f >/dev/null 2>&1
	podman rmi -f "localhost/${proj}-core" >/dev/null 2>&1
	rm -rf "$tmp"
}

on_exit() {
	teardown
	exit "$final_exit"
}
trap on_exit EXIT

step_fail() {
	# $1 exit code, $2 message
	echo "verify: $2" >&2
	final_exit="$1"
	exit "$1" # trap rewrites to final_exit
}

echo "verify: exporting $SOURCE_REF"
mkdir -p "$src"
git archive --format=tar "$SOURCE_REF" | tar -x -C "$src"
[ ! -e "$src/.git" ] || step_fail "$EXIT_TEARDOWN" "export unexpectedly contains .git"
[ ! -e "$src/.env" ] || step_fail "$EXIT_TEARDOWN" "export unexpectedly contains .env"
for banned in config/certs backend/out backend/telos-core frontend/.next frontend/out frontend/node_modules; do
	[ ! -e "$src/$banned" ] || step_fail "$EXIT_TEARDOWN" "export contains generated/local state: $banned"
done

echo "verify: generating disposable smoke environment"
storage="$tmp/storage"
mkdir -p "$storage/media" "$storage/staging" "$storage/bookdrop"
bootstrap_token="$(rand 24)"
smoke_password="telos-smoke-$(rand 12)"
umask 077
cat >"$src/.env" <<EOF
TELOS_DOMAIN=localhost
ACME_EMAIL=smoke@example.invalid
APP_UID=$(id -u)
APP_GID=$(id -g)
TZ=Etc/UTC
STORAGE_PATH=$storage
POSTGRES_USER=telos
POSTGRES_PASSWORD=$(rand 24)
POSTGRES_DB=telos
REDIS_PASSWORD=$(rand 24)
JELLYFIN_ADMIN_TOKEN=$(rand 16)
JELLYFIN_USER_NAME=
GRIMMORY_ADMIN_USER=telos-gateway
GRIMMORY_ADMIN_PASSWORD=$(rand 16)
GRIMMORY_DB_NAME=grimmory
GRIMMORY_DB_USER=grimmory
GRIMMORY_DB_PASSWORD=$(rand 16)
MARIADB_ROOT_PASSWORD=$(rand 16)
TELOS_BOOTSTRAP_TOKEN=$bootstrap_token
EOF
chmod 0600 "$src/.env"
umask 022

# The production compose file pins global network names; without overriding
# them the smoke project would join the live stack's networks and resolve
# service DNS names (postgres, redis) to production containers.
cat >"$tmp/smoke-overlay.yml" <<EOF
networks:
  telos-ingress:
    name: ${proj}-ingress
  telos-backend:
    name: ${proj}-backend
    internal: true
  telos-db:
    name: ${proj}-db
    internal: true
services:
  telos-core:
    image: localhost/${proj}-core
    container_name: ${proj}-core
    restart: "no"
    # The smoke stack has no TLS edge and is reached over a loopback port, so
    # it boots in development mode with the probe origin as the public origin;
    # the exact-origin policy then accepts the smoke probes' Origin header.
    environment:
      - TELOS_ENV=development
      - TELOS_PUBLIC_ORIGIN=http://127.0.0.1:${port}
      - TELOS_DEV_ORIGINS=http://127.0.0.1:${port}
    ports:
      - "127.0.0.1:${port}:8080"
  postgres:
    container_name: ${proj}-postgres
    restart: "no"
  redis:
    container_name: ${proj}-redis
    restart: "no"
EOF

echo "verify: validating compose model"
podman compose -p "$proj" --env-file "$src/.env" \
	-f "$src/docker-compose.yml" -f "$tmp/smoke-overlay.yml" \
	config --services >"$tmp/compose-services.txt" 2>"$tmp/compose-err.txt" ||
	step_fail "$EXIT_BOOT" "compose validation failed: $(cat "$tmp/compose-err.txt")"

echo "verify: building image from the export"
podman build -f "$src/backend/Dockerfile" -t "localhost/${proj}-core" "$src" \
	>"$tmp/build.log" 2>&1 ||
	step_fail "$EXIT_BUILD" "image build failed (see last lines): $(tail -5 "$tmp/build.log")"

echo "verify: booting isolated stack ($proj, port $port)"
podman compose -p "$proj" --env-file "$src/.env" \
	-f "$src/docker-compose.yml" -f "$tmp/smoke-overlay.yml" \
	up -d --no-build telos-core >"$tmp/up.log" 2>&1 ||
	step_fail "$EXIT_BOOT" "compose up failed: $(tail -5 "$tmp/up.log")"

probe_base="http://127.0.0.1:${port}"
if [ "$FAULT" = "readiness" ]; then
	probe_base="http://127.0.0.1:1" # injected fault: unreachable probe target
fi

echo "verify: waiting for gateway liveness"
health_status=""
for _ in $(seq 1 "${TELOS_VERIFY_PROBE_ATTEMPTS:-60}"); do
	health_status="$(curl -s -o "$tmp/health.json" -w '%{http_code}' \
		--max-time 3 "$probe_base/api/v1/health" || true)"
	case "$health_status" in 200 | 503) break ;; esac
	sleep 2
done
case "$health_status" in
200 | 503) ;;
*) step_fail "$EXIT_BOOT" "gateway liveness probe failed (last status: ${health_status:-none})" ;;
esac

echo "verify: probing static assets"
root_status="$(curl -s -o "$tmp/root.html" -w '%{http_code}' --max-time 5 "$probe_base/" || true)"
[ "$root_status" = "200" ] && grep -qi "<html" "$tmp/root.html" ||
	step_fail "$EXIT_BOOT" "static asset probe failed (status $root_status)"

echo "verify: probing bootstrap and login"
bs_status="$(curl -s -o "$tmp/bootstrap.out" -w '%{http_code}' --max-time 10 \
	-H 'Content-Type: application/json' -H "Origin: http://127.0.0.1:${port}" \
	-d "{\"username\":\"smokeowner\",\"password\":\"$smoke_password\",\"token\":\"$bootstrap_token\"}" \
	"$probe_base/api/v1/auth/bootstrap" || true)"
case "$bs_status" in
200 | 201) ;;
*) step_fail "$EXIT_BOOT" "bootstrap probe failed (status $bs_status)" ;;
esac
login_headers="$tmp/login-headers.txt"
login_status="$(curl -s -o "$tmp/login.out" -D "$login_headers" -w '%{http_code}' --max-time 10 \
	-H 'Content-Type: application/json' -H "Origin: http://127.0.0.1:${port}" \
	-d "{\"username\":\"smokeowner\",\"password\":\"$smoke_password\"}" \
	"$probe_base/api/v1/auth/login" || true)"
[ "$login_status" = "200" ] && grep -qi '^set-cookie: telos_session=' "$login_headers" ||
	step_fail "$EXIT_BOOT" "login probe failed (status $login_status)"

echo "verify: writing sanitized evidence"
cat >"$EVIDENCE_OUT" <<EOF
{
  "sourceRef": "$(git rev-parse "$SOURCE_REF")",
  "sourceTree": "$(git rev-parse "${SOURCE_REF}^{tree}")",
  "podman": "$(podman --version | tr -d '\n')",
  "composeServices": $(python3 -c 'import json,sys;print(json.dumps(sys.stdin.read().split()))' <"$tmp/compose-services.txt"),
  "builderImages": $(grep '^FROM ' "$src/backend/Dockerfile" | awk '{print $2}' | python3 -c 'import json,sys;print(json.dumps(sys.stdin.read().split()))'),
  "checks": {
    "inventory": "pass",
    "composeValidate": "pass",
    "imageBuild": "pass",
    "gatewayLiveness": "$health_status",
    "staticAssets": "$root_status",
    "bootstrap": "$bs_status",
    "login": "$login_status"
  }
}
EOF
chmod 0600 "$EVIDENCE_OUT"

if [ "$FAULT" = "teardown" ]; then
	echo "not-json" >"$EVIDENCE_OUT" # injected fault: corrupt evidence artifact
fi

python3 -m json.tool "$EVIDENCE_OUT" >/dev/null 2>&1 ||
	step_fail "$EXIT_TEARDOWN" "evidence artifact failed integrity check"

echo "verify: PASS ($proj) — evidence at $EVIDENCE_OUT"
