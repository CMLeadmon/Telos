#!/usr/bin/env bash
set -euo pipefail
repo_root="$(git rev-parse --show-toplevel)"
command -v ssh-keygen >/dev/null || { echo "signer-trust prerequisite missing: ssh-keygen" >&2; exit 1; }
python3 -B "$repo_root/tests/operations/signer_trust_test.py"
python3 -m py_compile "$repo_root/scripts/enroll-release-signer.py" "$repo_root/scripts/lib/signing.py"
