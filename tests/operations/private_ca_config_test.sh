#!/usr/bin/env bash
set -euo pipefail

# The private-CA static configuration must be the base Traefik configuration with only
# the ACME directory changed, and a private-CA node whose Traefik (UID 65532) cannot
# read its certificate material must be named before it starts, not discovered later
# as "unrecognized name" on a stack whose containers all report healthy.
repo_root="$(git rev-parse --show-toplevel)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
fail() { echo "private_ca_config_test: $*" >&2; exit 1; }

# 1. Drift guard: strip the comment-only header lines and the one intended change.
python3 - "$repo_root" <<'PY'
import sys, re
root = sys.argv[1]
base = open(f"{root}/config/traefik.yaml", encoding="utf-8").read()
private = open(f"{root}/config/private-ca/traefik.yaml", encoding="utf-8").read()
def normalise(text):
    out = []
    skip_comment_block = False
    for line in text.splitlines():
        if line.strip().startswith("#") or line.strip().startswith("caServer:"):
            continue
        out.append(line.rstrip())
    return "\n".join(out).strip()
assert normalise(base) == normalise(private), "config/private-ca/traefik.yaml drifted from config/traefik.yaml"
assert re.search(r"^\s+caServer: https://acme\.disabled\.invalid/directory$", private, re.M), "private-CA ACME directory must be unreachable"
assert "caServer" not in base, "the base configuration must use the public ACME directory"
PY

# 2. The overlay mounts the private static config over the base one.
grep -q 'config/private-ca/traefik.yaml:/etc/traefik/traefik.yaml' "$repo_root/docker-compose.private-ca.yml" \
  || fail "the overlay does not mount the private-CA static configuration"
! grep -q 'TRAEFIK_CERTIFICATESRESOLVERS' "$repo_root/docker-compose.private-ca.yml" \
  || fail "Traefik ignores environment variables when a static file exists; do not configure ACME that way"

# 3. private_ca_unreadable with shimmed id/setpriv.
mkdir -p "$work/bin" "$work/node" "$work/tls"
printf '#!/usr/bin/env bash\n[[ "$1" == -u ]] && { echo 0; exit 0; }\nexec /usr/bin/id "$@"\n' >"$work/bin/id"
cat >"$work/bin/setpriv" <<'SH'
#!/usr/bin/env bash
# setpriv --reuid=… --regid=… --clear-groups test FLAG PATH — unreadable when listed.
path="${!#}"
grep -qxF -- "$path" "$UNREADABLE_LIST" 2>/dev/null && exit 1
exit 0
SH
chmod +x "$work/bin/id" "$work/bin/setpriv"
printf 'TELOS_PRIVATE_CA=1\nTELOS_TLS_PATH=%s\n' "$work/tls" >"$work/node/node.env"
export PATH="$work/bin:$PATH" UNREADABLE_LIST="$work/unreadable"

probe() {
  (
    export CLI_DIR="$repo_root/cli" REPO_ROOT="$repo_root" TELOS_NODE_PATH="$work/node" TELOS_DEV=0
    # shellcheck disable=SC1091
    source "$repo_root/cli/lib/common.sh"; source "$repo_root/cli/lib/config.sh"
    node_path() { printf '%s' "$work/node"; }
    private_ca_unreadable
  )
}
: >"$UNREADABLE_LIST"
! probe >/dev/null || fail "readable material was reported as unreadable"
printf '%s\n' "$work/tls/server.key" >"$UNREADABLE_LIST"
[[ "$(probe)" == "$work/tls/server.key" ]] || fail "an unreadable key was not named"
printf '%s\n' "$work/tls" >"$UNREADABLE_LIST"
[[ "$(probe)" == "$work/tls" ]] || fail "an untraversable directory was not named"
printf 'TELOS_PRIVATE_CA=\n' >"$work/node/node.env"
! probe >/dev/null || fail "a public-ACME node must not be checked"
# 4. Key-sharing rules: root-only, or one group that is Traefik's, never anyone else.
python3 -B - "$repo_root" <<'PY'
import os, stat, sys, tempfile
sys.path.insert(0, sys.argv[1] + "/cli/lib")
import node_config as nc
directory = tempfile.mkdtemp()
for name in ("server.crt", "server.key"):
    open(os.path.join(directory, name), "w").close()
    os.chmod(os.path.join(directory, name), 0o644)
values = {"TELOS_PRIVATE_CA": "1", "TELOS_TLS_PATH": directory}
real_stat = os.stat
def check(mode, gid):
    key = os.path.join(directory, "server.key")
    os.chmod(key, mode)
    def fake(path, *a, **k):
        info = real_stat(path, *a, **k)
        if path == key:
            return os.stat_result((info.st_mode, info.st_ino, info.st_dev, info.st_nlink, info.st_uid, gid, info.st_size,
                                   int(info.st_atime), int(info.st_mtime), int(info.st_ctime)))
        return info
    nc.os.stat = fake
    try:
        return nc.validate_private_ca(values)
    finally:
        nc.os.stat = real_stat
assert check(0o600, os.getgid()) == [], "0600 must be accepted"
assert check(0o400, os.getgid()) == [], "0400 must be accepted"
assert check(0o640, 65532) == [], "0640 for Traefik's group must be accepted"
assert check(0o640, 0) == [], "0640 for root's group must be accepted"
assert check(0o640, 1234), "0640 for an unrelated group must be rejected"
assert check(0o644, 65532), "world-readable must be rejected"
assert check(0o660, 65532), "group-writable must be rejected"
assert check(0o604, 65532), "other access must be rejected"
PY
echo "private CA: static configuration parity, unreachable ACME directory, unreadable key/directory named before start"
