#!/usr/bin/env bash
# Product-truth guard: fail if a prohibited or inert surface reappears in the
# SHIPPED product — an Oracle/AI control, an AI product claim, an AI API/runtime
# dependency, an OIDC-as-shipped claim, a non-Apache Telos license claim, an
# absolute local-only privacy claim, or a listed visible inert control.
#
# Historical plans/specs (docs/superpowers/**, any path with /plans/ or /specs/)
# are excluded — they legitimately discuss removed ideas. This is a guardrail,
# not a proof; it scans code, manifests, and user-facing copy for concrete
# prohibited tokens.
#
# Exit 0 clean, 1 a prohibited/inert surface was found.
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT" || exit 2

fail=0
report() { echo "product-truth: VIOLATION ($1):" >&2; echo "$2" | sed 's/^/  /' >&2; fail=1; }

# 1. Oracle/AI control or surface in shipped code (icons, controls, styles, roles).
hit=$(grep -rniE 'oracle|isOracle|ai[ -]?oracle' frontend/src backend 2>/dev/null \
      | grep -vE '(_test\.go|\.spec\.ts)')
[ -n "$hit" ] && report "Oracle/AI control or surface" "$hit"

# 2. AI API/runtime dependency in a manifest.
hit=$(grep -rniE 'openai|@anthropic|anthropic-sdk|langchain|cohere|replicate|generative-ai|huggingface|ollama|@google/generative' \
      frontend/package.json frontend/package-lock.json backend/go.mod backend/go.sum 2>/dev/null)
[ -n "$hit" ] && report "AI API/runtime dependency" "$hit"

# 3. Positive AI product claim in user-facing copy.
hit=$(grep -rniE 'AI[ -]powered|powered by AI|AI (summar|assistant|feature|module|oracle)|smart summar|AI-generated' \
      frontend/src README.md 2>/dev/null | grep -viE 'no |without|removed|not (an?|our)')
[ -n "$hit" ] && report "AI product claim" "$hit"

# 4. OIDC-as-shipped claim in user-facing copy.
hit=$(grep -rniE 'sign ?in with oidc|oidc login|oidc[- ]enabled|oidc federation (is|now) (available|supported|enabled)' \
      frontend/src README.md 2>/dev/null)
[ -n "$hit" ] && report "OIDC-as-shipped claim" "$hit"

# 5. Non-Apache Telos license claim.
if ! grep -qiE 'apache license' LICENSE 2>/dev/null; then
  report "license" "LICENSE does not carry the Apache License text"
fi
hit=$(grep -rniE 'telos is (mit|gpl|bsd|proprietary)|licensed under (the )?(mit|gpl|bsd)' README.md 2>/dev/null)
[ -n "$hit" ] && report "non-Apache Telos license claim" "$hit"

# 6. Absolute local-only privacy claim in user-facing copy (negations excluded).
hit=$(grep -rniE 'nothing (ever )?leaves|never leaves (your|the) (node|device|server)|fully offline|100% private|completely private|no data ever leaves|air-?gapped' \
      frontend/src README.md 2>/dev/null | grep -viE 'no |not |does not|without|never claims')
[ -n "$hit" ] && report "absolute local-only privacy claim" "$hit"

# 7. Listed visible inert controls that must stay absent until their tasks ship.
#    The Oracle control is permanently prohibited; the notification bell shipped
#    in P5-T3, so it is no longer inert.
hit=$(grep -rniE 'aria-label="oracle"' frontend/src 2>/dev/null)
[ -n "$hit" ] && report "inert advertised control" "$hit"

# 8. Removed feature surfaces must not reappear in shipped code (voice rooms,
#    LiveKit, Watch Party, the notification inbox, or My List). Tests and
#    historical plans/specs are excluded above by path; here we scope to source.
hit=$(grep -rniE 'livekit|watch.?party|voice.?dock|useVoiceSession|useWatchParty|useNotificationStore|useMyListStore|/media-list' \
      frontend/src backend --include='*.go' --include='*.ts' --include='*.tsx' 2>/dev/null \
      | grep -vE '(_test\.go|\.spec\.ts|\.test\.tsx?)')
[ -n "$hit" ] && report "removed feature surface reappeared" "$hit"

if [ "$fail" -eq 0 ]; then
  echo "product-truth: OK — no prohibited or inert surface found."
fi
exit "$fail"
