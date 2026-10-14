#!/usr/bin/env bash
# reproducible-build-test.sh -- prove the release builder is reproducible: the same commit, built twice in different
# directories, time zones, locales and umasks (the second time without a build cache), yields byte-identical candidates
# (every file, so the graph documents, the lock and the SBOMs too).
#
# It runs the real builder (scripts/build-release.sh --rehearsal ... --verify-reproducible is the in-build form of this
# check; this is the cross-environment form) and so needs Podman, skopeo, zstd, network access for the locked base images
# and the locked Syft. Without them it exits 3 (not_run) -- it never passes by skipping.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

for tool in podman skopeo zstd git python3; do
  command -v "$tool" >/dev/null 2>&1 || { echo "reproducible-build: not_run: $tool is not installed" >&2; exit 3; }
done
commit="$(git rev-parse HEAD)"
version="$(python3 -c 'import json; print(json.load(open("frontend/package.json"))["version"])')"
work="$(mktemp -d)"
cleanup() { rm -rf "${work:?}"; }
trap cleanup EXIT

echo "reproducible-build: building $version at ${commit:0:12} (first build)"
( umask 022; TZ=UTC LC_ALL=C bash scripts/build-release.sh --rehearsal --source-commit "$commit" --version "$version" --out-dir "$work/one" ) >"$work/one.log" 2>&1 \
  || { tail -20 "$work/one.log" >&2; echo "reproducible-build: the first build failed" >&2; exit 1; }

echo "reproducible-build: building again elsewhere, in another time zone, locale and umask, with no cache"
( umask 077; TZ=Pacific/Auckland LC_ALL=C.UTF-8 TELOS_BUILD_NO_CACHE=1 bash scripts/build-release.sh --rehearsal --source-commit "$commit" --version "$version" --out-dir "$work/two/deeper" ) >"$work/two.log" 2>&1 \
  || { tail -20 "$work/two.log" >&2; echo "reproducible-build: the second build failed" >&2; exit 1; }

if ! python3 scripts/lib/build_release.py compare --a "$work/one/candidate" --b "$work/two/deeper/candidate"; then
  echo "reproducible-build: FAIL -- the two builds differ" >&2
  exit 1
fi
echo "reproducible-build: PASS -- $(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["candidateLockDigest"])' "$work/one/build-record.json") built identically twice"
