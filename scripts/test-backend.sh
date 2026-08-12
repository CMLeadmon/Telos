#!/usr/bin/env bash
# test-backend.sh — run backend Go tests with a disposable PostgreSQL + Redis.
#
# Usage:
#   scripts/test-backend.sh [suite] [extra go test args...]
#
# The optional first argument is a convenience label (auth|realtime|security|
# all) that selects a -run filter; any remaining arguments pass straight to
# `go test`. Integration tests read TELOS_TEST_DATABASE_URL/TELOS_TEST_REDIS_URL
# and skip themselves when those are unset, so a plain `go test ./...` still
# works without this harness.
#
# Requires podman on the host. Ephemeral containers/network are always removed.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

GO_IMAGE="docker.io/library/golang:1.26.5"
PG_IMAGE="docker.io/library/postgres:16.14-alpine"
REDIS_IMAGE="docker.io/library/redis:7-alpine"
if test -f release/images.lock; then
	# images.lock is JSON and carries no "golang-test" key, so this lookup can
	# legitimately come back empty. Only override the default when it does not,
	# otherwise podman is handed an empty image reference and the whole suite
	# dies with "repository name must have at least one component".
	locked="$(awk '$1 == "golang-test" { print $2 }' release/images.lock)"
	test -n "$locked" && GO_IMAGE="$locked"
fi

suite="${1:-all}"
run_filter=""
case "$suite" in
# De-anchored for the same reason the product selector was: keywords have to
# match anywhere in the name. Anchored, this selector ran none of the ~20 device,
# token and WS-ticket tests — every one of them is named TestRegisterDevice…,
# TestWSTicket… or TestGetAuthenticatedUserBearerBranch, so `test-backend.sh auth`
# reported green over the entire device-authentication surface without touching it.
auth) run_filter="-run (Bootstrap|InviteAcceptance|LastOwner|LoginLimiter|CanonicalUsername|SessionMutation|SecurityEvent|Device|WSTicket|SecureToken|BearerBranch|SessionTokenFromRequest|CurrentTokenHash)"; shift ;;
realtime) run_filter="-run Test(Channel|Realtime|Socket|Revocation|Subscription)"; shift ;;
security) run_filter="-run Test(Server|Shutdown|Slow|HTTPAdmission|Security)"; shift ;;
db) run_filter="-run Test(Migration|Migrations|Migrator|Database|ForeignKey|QueryStatistics|MessageManagement|Cursor|ListPolicy|Search|Outbox|AccountDeletion|RetentionMatrix|AssetDeletion|SchemaOwner|RuntimeRole|LoadAuthenticatedUser|HistoryQueryCount|BuildListQuery|SessionTouchWorker|DatabaseTimeouts|DatabaseConstraints|FailedMigration|ConcurrentMigrators|DiscoverMigrations|PlanMigrations|ChecksumSet)"; shift ;;
storage) run_filter="-run Test(FileStore|FilePurpose|FileAudit|LogicalFolder|IngestionLease|BookHandoff|Upload|Quota|Capacity|Storage|Confine|EPUB|ContentValidation|ClamAV|Reconcil|Jellyfin|Grimmory|Range|Readiness|Health|Egress)"; shift ;;
# Keywords match anywhere in the test name, not only straight after "Test".
# Anchored, this selected 3 of the 12 annotation tests: TestModerateAnnotation…,
# TestAuthorizeAnnotationTarget… and the rest were silently skipped, so a
# selector run could report a green product suite having never exercised them.
product) run_filter="-run (UserEvent|Thread|Annotation|Commentary|ChannelOverride)"; shift ;;
all) shift || true ;;
-*) : ;; # first arg is already a go-test flag
*) shift || true ;;
esac

rand="$(od -An -N3 -tx1 /dev/urandom | tr -d ' \n')"
proj="telostest${rand}"
net="${proj}-net"

cleanup() {
	podman rm -f "${proj}-pg" "${proj}-redis" >/dev/null 2>&1 || true
	podman network rm -f "$net" >/dev/null 2>&1 || true
}
trap cleanup EXIT

podman network create "$net" >/dev/null

podman run -d --name "${proj}-pg" --network "$net" \
	-e POSTGRES_USER=telos -e POSTGRES_PASSWORD=testpw -e POSTGRES_DB=telos_test \
	"$PG_IMAGE" >/dev/null
podman run -d --name "${proj}-redis" --network "$net" "$REDIS_IMAGE" >/dev/null

echo "test-backend: waiting for PostgreSQL and Redis"
for _ in $(seq 1 60); do
	if podman exec "${proj}-pg" pg_isready -U telos -d telos_test >/dev/null 2>&1 &&
		podman exec "${proj}-redis" redis-cli ping >/dev/null 2>&1; then
		break
	fi
	sleep 1
done

# Reuse a module cache across runs so ephemeral containers don't re-download
# the module graph every time (the volume is created on first use).
podman volume exists telos-go-cache >/dev/null 2>&1 || podman volume create telos-go-cache >/dev/null

podman run --rm --network "$net" -v ./backend:/app:z -w /app \
	-v telos-go-cache:/go \
	-e TELOS_TEST_DATABASE_URL="postgres://telos:testpw@${proj}-pg:5432/telos_test?sslmode=disable" \
	-e TELOS_TEST_REDIS_URL="redis://${proj}-redis:6379/0" \
	"$GO_IMAGE" go test $run_filter "$@" ./...
