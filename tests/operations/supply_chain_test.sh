#!/usr/bin/env bash
set -euo pipefail

# The supply-chain fixture suite (the `supply-chain-policy` gate): locked-tool verification, risk acceptances,
# vulnerability/secret/license policy, SBOM generation and semantic validation, the scan's gate-result, and the
# shipped policy files -- plus one LIVE check that the locked gitleaks really detects a planted secret and stays
# quiet on clean text. That live step needs the locked archive (downloaded and digest-verified into
# $TELOS_CI_TOOLS_DIR, default ~/.cache/telos-ci-tools); without network or a cached copy the suite exits 3
# (not_run) after everything hermetic has passed.
repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"
python3 -I tests/operations/supply_chain_test.py
python3 -m py_compile scripts/bootstrap-ci-tools.py scripts/lib/supply_chain.py
python3 -I scripts/lib/supply_chain.py policy
bash scripts/verify-release-pins.sh >/dev/null

tools="${TELOS_CI_TOOLS_DIR:-$HOME/.cache/telos-ci-tools}"
if ! gitleaks="$(python3 -I scripts/bootstrap-ci-tools.py --tool gitleaks --cache-dir "$tools" 2>/dev/null)"; then
  echo "supply-chain: not_run: the locked gitleaks could not be fetched and verified (offline?)" >&2
  exit 3
fi
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir "$work/dirty" "$work/clean"
# A GitHub personal access token shape; the value is random test data, not a credential.
printf 'token = ghp_%s\n' "$(python3 -c 'import secrets, string; print("".join(secrets.choice(string.ascii_letters + string.digits) for _ in range(36)))')" >"$work/dirty/config.txt"
printf 'nothing secret here\n' >"$work/clean/readme.txt"
"$gitleaks" dir "$work/dirty" --no-banner --report-format json --report-path "$work/dirty.json" --exit-code 0 >/dev/null 2>&1
"$gitleaks" dir "$work/clean" --no-banner --report-format json --report-path "$work/clean.json" --exit-code 0 >/dev/null 2>&1
python3 -I - "$work/dirty.json" "$work/clean.json" "$repo_root" <<'PY'
import json, sys
sys.path.insert(0, sys.argv[3] + "/scripts/lib")
import supply_chain as sc
dirty, clean = (json.load(open(path)) for path in sys.argv[1:3])
assert len(sc.evaluate_secrets(dirty)) >= 1, "the locked gitleaks missed a planted token"
assert sc.evaluate_secrets(clean) == [], clean
PY
echo "supply-chain fixtures: tools, acceptances, vulnerability/secret/license policy, SBOM validation, scan result and a live secret detection verified"
