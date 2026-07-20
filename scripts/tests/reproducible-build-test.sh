#!/usr/bin/env bash
# reproducible-build-test.sh — prove two builds of unchanged source agree.
#
# Builds the telos-core image twice (the second with --no-cache so every
# layer re-runs), then requires:
#   - identical image configuration (env, cmd, workdir, ports) — the image
#     Created timestamp is the explicitly normalized exception;
#   - identical Go dependency manifests (go version -m);
#   - identical embedded frontend inventory, proven by identical binary
#     checksums (the export is //go:embed'ed into telos-core).
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

tmp="$(mktemp -d)"
cleanup() {
	rm -rf "$tmp"
	podman rmi -f telos-repro-a telos-repro-b >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "repro: building image A"
podman build -q -f backend/Dockerfile -t telos-repro-a . >/dev/null
echo "repro: building image B (--no-cache)"
podman build -q --no-cache -f backend/Dockerfile -t telos-repro-b . >/dev/null

fail=0

config_a="$(podman image inspect --format '{{json .Config}}' telos-repro-a)"
config_b="$(podman image inspect --format '{{json .Config}}' telos-repro-b)"
if [ "$config_a" != "$config_b" ]; then
	echo "repro: image configurations differ" >&2
	diff <(printf '%s' "$config_a") <(printf '%s' "$config_b") >&2 || true
	fail=1
fi

for img in a b; do
	ctr="$(podman create "telos-repro-$img")"
	podman cp "$ctr:/app/telos-core" "$tmp/telos-core-$img"
	podman rm "$ctr" >/dev/null
done

sum_a="$(sha256sum "$tmp/telos-core-a" | awk '{print $1}')"
sum_b="$(sha256sum "$tmp/telos-core-b" | awk '{print $1}')"

go_image="docker.io/library/golang:1.26.5"
for img in a b; do
	podman run --rm -v "$tmp:/x:z" "$go_image" \
		go version -m "/x/telos-core-$img" | sed '1d' >"$tmp/modules-$img.txt"
done
if ! diff -u "$tmp/modules-a.txt" "$tmp/modules-b.txt"; then
	echo "repro: Go dependency manifests differ" >&2
	fail=1
fi

if [ "$sum_a" != "$sum_b" ]; then
	echo "repro: telos-core binaries differ ($sum_a vs $sum_b) — embedded" \
		"frontend inventory or build inputs are not reproducible" >&2
	fail=1
fi

if [ "$fail" -ne 0 ]; then
	exit 1
fi
echo "repro: PASS — identical config, dependency manifest, and embedded inventory ($sum_a)"
