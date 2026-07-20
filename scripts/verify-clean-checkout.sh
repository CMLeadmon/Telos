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
#   2  usage error / unimplemented mode
#   10 tracked/input inventory failed
#   20 build or test failed
#   30 compose boot/readiness failed
#   40 teardown or artifact-integrity failed
#
# Inventory mode checks the current index and working tree by default; it
# never silently substitutes a previous HEAD. Supplying --source-ref checks
# that exact tree-ish instead.
set -euo pipefail

EXIT_OK=0
EXIT_USAGE=2
EXIT_INVENTORY=10

MODE=""
SOURCE_REF=""
EVIDENCE_OUT=""

usage() {
	sed -n '2,16p' "$0" >&2
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

	# Production compose: only Traefik may publish HTTP entry points. Other
	# published ports must be on the reviewed non-HTTP allowlist (LiveKit
	# WebRTC/TURN media ports).
	local port_report
	port_report="$(awk '
		/^services:/ { in_services=1; next }
		in_services && /^[a-zA-Z0-9_-]+:/ { in_services=0 }
		/^  [a-zA-Z0-9_-]+:/ { service=$1; sub(":", "", service); in_ports=0 }
		/^    ports:/ { in_ports=1; next }
		in_ports && /^      - / {
			port=$2; gsub(/"/, "", port);
			print service " " port; next
		}
		in_ports && !/^      / { in_ports=0 }
	' docker-compose.yml)"
	while IFS=' ' read -r service port; do
		[ -n "$service" ] || continue
		case "$service:$port" in
		traefik:80:80 | traefik:443:443) ;;
		livekit:7881:7881 | livekit:3478:3478/udp | livekit:50000-50100:50000-50100/udp) ;;
		*) fail_inventory "unreviewed published port in production compose: service=$service port=$port" ;;
		esac
	done <<<"$port_report"

	# Development overlay bindings must be loopback-only.
	if [ -f docker-compose.dev.yml ]; then
		local dev_ports
		dev_ports="$(grep -E '^\s+- "' docker-compose.dev.yml | tr -d ' "-' || true)"
		while IFS= read -r p; do
			[ -n "$p" ] || continue
			case "$p" in
			127.0.0.1:*) ;;
			*) fail_inventory "development overlay binding is not loopback-only: $p" ;;
			esac
		done <<<"$dev_ports"
	fi

	# Pinned-toolchain assertions: no build input may float.
	grep -qE '^go 1\.26\.5$' backend/go.mod ||
		fail_inventory "backend/go.mod does not pin go 1.26.5"
	[ -f .nvmrc ] && [ "$(cat .nvmrc)" = "24.18.0" ] ||
		fail_inventory ".nvmrc does not pin Node.js 24.18.0"
	grep -q 'FROM node:24\.18\.0-alpine' backend/Dockerfile ||
		fail_inventory "backend/Dockerfile does not pin node:24.18.0-alpine"
	grep -q 'FROM golang:1\.26\.5-alpine' backend/Dockerfile ||
		fail_inventory "backend/Dockerfile does not pin golang:1.26.5-alpine"
	grep -q 'image: postgres:16\.14-alpine' docker-compose.yml ||
		fail_inventory "docker-compose.yml does not pin postgres:16.14-alpine"
	grep -q '"next": "16\.2\.10"' frontend/package.json ||
		fail_inventory "frontend/package.json does not pin next 16.2.10 exactly"
	grep -q '"postcss": "8\.5\.10"' frontend/package.json ||
		fail_inventory "frontend/package.json does not pin the postcss 8.5.10 override"
	grep -q '"epubjs": "0\.4\.2"' frontend/package.json ||
		fail_inventory "frontend/package.json does not pin epubjs 0.4.2"
	if grep -nE '"(dependencies|devDependencies)"' -A 40 frontend/package.json |
		grep -E '": "[\^~]' >/dev/null; then
		fail_inventory "frontend/package.json contains floating (^/~) dependency ranges"
	fi

	if [ "$INVENTORY_FAILED" -ne 0 ]; then
		exit "$EXIT_INVENTORY"
	fi
	echo "inventory: all required release inputs present ($count migrations, contiguous)"
}

case "$MODE" in
inventory)
	run_inventory
	exit "$EXIT_OK"
	;;
"")
	# Full mode (build/boot/evidence) is completed in P1-T5.
	echo "full verification mode is not implemented yet (P1-T5)" >&2
	exit "$EXIT_USAGE"
	;;
esac
