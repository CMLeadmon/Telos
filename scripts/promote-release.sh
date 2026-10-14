#!/usr/bin/env bash
# promote-release.sh -- publish a verified draft prerelease WITHOUT building, signing, renaming or uploading anything.
#
#   scripts/promote-release.sh --tag vX.Y.Z-beta.N --candidate-lock-digest sha256:HEX
#       [--catalog ci/phase-gates.json] [--signers release/evidence-signers] [--repo DIR]
#
# Downloads every asset of the draft, verifies the signed tag, the candidate graph and checksum, the evidence bundle
# (fresh at promotion time), the certification, a different principal's signed review, that the candidate is the
# exact digest given, and that the asset list is exactly the verified inventory; only then runs
#   gh release edit TAG --draft=false --prerelease=true
# and compares the published assets with the verified draft. Exit 3 while release/publisher-policy.json is unreviewed.
set -euo pipefail
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec python3 -I "$script_dir/lib/stage_release.py" promote "$@"
