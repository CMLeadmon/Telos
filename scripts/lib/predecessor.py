#!/usr/bin/env python3
"""The predecessor policy: which exact earlier release an upgrade is certified from.

    predecessor.py check [--policy FILE] [--repo DIR]   print the verdict as JSON; exit 0 only when a predecessor is buildable

A predecessor is named by its signed tag, its source commit, the digest of its candidate lock and the
digest of its generation manifest -- never by a version string alone. The verdict is one of:
  ready      the commit is in this repository and the policy is complete (building still needs the
             release builder, which reports its own not_run when unavailable)
  not_run    no predecessor is named, or the named commit cannot be obtained
  invalid    the policy is malformed (a version-only assertion is invalid)
"""

import argparse
import json
import os
import re
import subprocess
import sys

DEFAULT_POLICY = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "release", "predecessor-policy.json")
DIGEST = re.compile(r"sha256:[0-9a-f]{64}\Z")
FIELDS = ("version", "sourceTag", "sourceCommit", "candidateLockDigest", "generationManifestDigest")


def verdict(policy, repo):
    if not isinstance(policy, dict) or policy.get("schemaVersion") != 1:
        return "invalid", "the predecessor policy is not a schemaVersion 1 object"
    for key in ("maxSchemaMigration", "allowInPlaceRollback"):
        if key not in policy:
            return "invalid", f"the predecessor policy lacks {key}"
    if not isinstance(policy["maxSchemaMigration"], int) or isinstance(policy["maxSchemaMigration"], bool) or policy["maxSchemaMigration"] < 0:
        return "invalid", "maxSchemaMigration must be a natural number"
    predecessor = policy.get("predecessor")
    if predecessor is None:
        return "not_run", "the predecessor policy names no obtainable exact predecessor (no release has been published)"
    if not isinstance(predecessor, dict) or set(predecessor) != set(FIELDS):
        return "invalid", f"a predecessor needs exactly {', '.join(FIELDS)}; a version-only assertion is not accepted"
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+-beta\.[1-9][0-9]*", str(predecessor["version"])):
        return "invalid", "the predecessor version is not a beta version"
    if predecessor["sourceTag"] != "v" + predecessor["version"]:
        return "invalid", "sourceTag must be 'v' plus the version"
    if not re.fullmatch(r"[0-9a-f]{40}", str(predecessor["sourceCommit"])):
        return "invalid", "sourceCommit must be 40 hex characters"
    for key in ("candidateLockDigest", "generationManifestDigest"):
        if not DIGEST.match(str(predecessor[key])):
            return "invalid", f"{key} must be sha256: followed by 64 hex characters"
    probe = subprocess.run(["git", "-C", repo, "cat-file", "-e", predecessor["sourceCommit"] + "^{commit}"], capture_output=True, check=False)
    if probe.returncode != 0:
        return "not_run", f"the predecessor commit {predecessor['sourceCommit'][:12]} is not in this repository"
    return "ready", f"predecessor {predecessor['version']} is obtainable at {predecessor['sourceCommit'][:12]}; the release builder is still required to build it"


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)
    check = sub.add_parser("check")
    check.add_argument("--policy", default=DEFAULT_POLICY)
    check.add_argument("--repo", default=os.path.join(os.path.dirname(DEFAULT_POLICY), ".."))
    args = parser.parse_args(argv)
    try:
        with open(args.policy, "rb") as handle:
            policy = json.loads(handle.read().decode("utf-8"))
    except (OSError, ValueError) as error:
        print(json.dumps({"verdict": "invalid", "reason": f"cannot read the predecessor policy ({error.__class__.__name__})"}))
        return 2
    state, reason = verdict(policy, args.repo)
    print(json.dumps({"verdict": state, "reason": reason}, sort_keys=True))
    return {"ready": 0, "not_run": 3, "invalid": 2}[state]


if __name__ == "__main__":
    sys.exit(main())
