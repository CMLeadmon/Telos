#!/usr/bin/env bash
set -euo pipefail

# The desktop artifact verifier against fixture AppImages. The payload cases need
# squashfs-tools (mksquashfs/unsquashfs); without them the suite reports not_run
# (exit 3) rather than passing on a suffix check.
repo_root="$(git rev-parse --show-toplevel)"
if ! command -v mksquashfs >/dev/null || ! command -v unsquashfs >/dev/null; then
  echo "not_run: squashfs-tools (mksquashfs, unsquashfs) are required to build and inspect fixture AppImages" >&2
  exit 3
fi
python3 -I "$repo_root/tests/operations/desktop_artifact_test.py"
echo "desktop artifact: AppImage shape, identity, payload and candidate binding verified without executing the artifact"
