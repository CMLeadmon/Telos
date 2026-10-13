#!/usr/bin/env bash
# verify-release-pins.sh — strict wrapper over the image-lock source verification.
#
# Every external FROM and Compose image must be fully qualified, pinned by digest and
# mapped to exactly one reviewed entry of release/images.lock. There is no
# "the lock file exists" success: a fabricated digest, a mutable tag or an unreviewed
# image fails. Runtime verification (the effective containers) is
# `scripts/verify-image-lock.py --runtime`.
set -euo pipefail
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec python3 -I "$script_dir/verify-image-lock.py" --source "$@"
