#!/usr/bin/env bash
# check-release-truth.sh — fail on any false legal or product claim.
#
# Exits nonzero for:
#   - a non-Apache Telos license claim (or a modified LICENSE text)
#   - a local or generic repository link on a product/legal surface
#   - a nonexistent tunnel/service claim or absolute local-only privacy claim
#   - a missing isolated-service credit
#   - a mismatch between CREDITS.md and its in-app UI projection
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

fail=0
err() {
	echo "release-truth: $1" >&2
	fail=1
}

# --- Telos license claims ---------------------------------------------------
apache_sha="cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30"
if [ ! -f LICENSE ] || [ "$(sha256sum LICENSE | awk '{print $1}')" != "$apache_sha" ]; then
	err "LICENSE is missing or is not the unmodified Apache License 2.0 text"
fi
[ -f NOTICE ] || err "NOTICE is missing"
[ -f CREDITS.md ] || err "CREDITS.md is missing"

grep -q "Apache License 2.0" README.md || err "README.md does not state the Apache-2.0 license"
if grep -nE '"v[^"]*">(AGPL|GPL|MIT|BSD)' frontend/src/app/page.tsx >/dev/null; then
	err "landing page claims a non-Apache Telos license"
fi
grep -q "Apache-2.0" frontend/src/app/page.tsx || err "landing page does not state Apache-2.0"
if grep -rniE "Telos (is|gateway.{0,40}) licen[sc]ed under (the )?(MIT|GPL|AGPL|BSD)" README.md documentation/ frontend/src >/dev/null; then
	err "found a non-Apache Telos license claim"
fi

# --- Repository links -------------------------------------------------------
if grep -rn "file:///" README.md CREDITS.md NOTICE documentation/ frontend/src/app/page.tsx >/dev/null; then
	err "found a local file:// link on a product/legal surface"
fi
if grep -rnE 'href="https://github\.com/?"' frontend/src >/dev/null; then
	err "found a generic github.com link in the frontend"
fi
if grep -rnE '\]\(https://github\.com/?\)|\bhttps://github\.com/?($|[^A-Za-z0-9_/-])' README.md CREDITS.md >/dev/null; then
	err "found a generic github.com link in README/CREDITS"
fi

# --- Privacy and nonexistent-service claims ---------------------------------
if grep -rniE "encrypted tunnel|nothing (ever )?leaves the node unless|never leaves (the|your) (node|server)" frontend/src README.md >/dev/null; then
	err "found a nonexistent-tunnel or absolute local-only privacy claim"
fi

# --- AI/Oracle marketing claims ----------------------------------------------
# (The full AI-surface absence scan across the app is Phase 5's P5-T1.)
if grep -rniE "\boracle\b|AI (summar|assistant|analysis)|an AI that" \
	frontend/src/app/page.tsx README.md >/dev/null; then
	err "found an AI/Oracle marketing claim"
fi

# --- Isolated-service credits -----------------------------------------------
for svc in Jellyfin Grimmory Traefik PostgreSQL Redis MariaDB ClamAV; do
	grep -q "| $svc " CREDITS.md || err "CREDITS.md is missing isolated service/infrastructure credit: $svc"
done

# --- CREDITS.md vs in-app projection ----------------------------------------
canonical="$(awk -F'|' '/^\| [A-Z]/ {
	name=$2; src=$4; lic=$5
	gsub(/^ +| +$/, "", name)
	if (name == "Name") next
	match(src, /https:\/\/[^)\]]+/); srcurl=substr(src, RSTART, RLENGTH)
	match(lic, /\[[^\]]+\]/); licname=substr(lic, RSTART+1, RLENGTH-2)
	print name "\t" srcurl "\t" licname
}' CREDITS.md | sort)"
projection="$(awk '
	/name: "/    { match($0, /"[^"]+"/); name=substr($0, RSTART+1, RLENGTH-2) }
	/sourceUrl: "/ { match($0, /"[^"]+"/); src=substr($0, RSTART+1, RLENGTH-2) }
	/licenseName: "/ { match($0, /"[^"]+"/); lic=substr($0, RSTART+1, RLENGTH-2)
		print name "\t" src "\t" lic }
' frontend/src/components/settings/CreditsSection.tsx | sort)"
if [ "$canonical" != "$projection" ]; then
	err "CREDITS.md and the in-app Credits projection disagree"
	diff <(printf '%s\n' "$canonical") <(printf '%s\n' "$projection") >&2 || true
fi

if [ "$fail" -ne 0 ]; then
	exit 1
fi
echo "release-truth: all legal/product truth checks passed"
