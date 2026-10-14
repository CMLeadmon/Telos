#!/usr/bin/env bash
# generate-sbom.sh -- one SPDX JSON SBOM per immutable OCI/desktop artifact, with the locked Syft.
#
#   scripts/generate-sbom.sh --payload-index PATH --artifact-root DIR --out-dir DIR [--tools-dir DIR]
#
# The payload index is the pre-lock artifact index (content artifacts only; no SBOMs, no graph metadata). Every
# artifact is re-verified against it first. SBOMs are named for the artifact digest, bound to it by document
# name, and never contain the candidate-lock digest. Tools come from ci/tools.lock through
# scripts/bootstrap-ci-tools.py (default cache: $TELOS_CI_TOOLS_DIR or ~/.cache/telos-ci-tools).
set -euo pipefail
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
payload="" artifacts="" out="" tools="${TELOS_CI_TOOLS_DIR:-$HOME/.cache/telos-ci-tools}"
usage() { echo "usage: $0 --payload-index PATH --artifact-root DIR --out-dir DIR [--tools-dir DIR]" >&2; exit 2; }
while [[ $# -gt 0 ]]; do
  case "$1" in
    --payload-index) [[ $# -ge 2 && -z "$payload" && "$2" != --* ]] || usage; payload="$2"; shift 2 ;;
    --artifact-root) [[ $# -ge 2 && -z "$artifacts" && "$2" != --* ]] || usage; artifacts="$2"; shift 2 ;;
    --out-dir) [[ $# -ge 2 && -z "$out" && "$2" != --* ]] || usage; out="$2"; shift 2 ;;
    --tools-dir) [[ $# -ge 2 && "$2" != --* ]] || usage; tools="$2"; shift 2 ;;
    *) usage ;;
  esac
done
[[ -n "$payload" && -n "$artifacts" && -n "$out" ]] || usage
# Go module licenses come from the module proxy; without it every Go dependency reads as unlicensed.
export SYFT_GOLANG_SEARCH_REMOTE_LICENSES=true
exec python3 -I "$script_dir/lib/supply_chain.py" generate-sbom --payload-index "$payload" --artifact-root "$artifacts" --out-dir "$out" --tools-dir "$tools"
