#!/usr/bin/env python3
"""The predecessor policy: which exact earlier release an upgrade is certified from.

    predecessor.py check [--policy FILE] [--repo DIR]   print the verdict as JSON; exit 0 only when a predecessor is buildable
    predecessor.py build --out-dir ABS [--policy FILE] [--repo DIR] [--desktop-dir DIR]
                                                        rebuild it with its own builder; exit 0 only to the exact recorded digests

A predecessor is named by its signed tag, its source commit, the digest of its candidate lock and the
digest of its generation manifest -- never by a version string alone. The verdict is one of:
  ready      the commit is in this repository and the policy is complete (building still needs the
             release builder, which reports its own not_run when unavailable)
  not_run    no predecessor is named, or the named commit cannot be obtained
  invalid    the policy is malformed (a version-only assertion is invalid)
"""

import argparse
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile

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


def build(policy, repo, out_dir, desktop_dir=None, tools_dir=None, builder=None):
    """Rebuild the exact predecessor with ITS OWN builder, from its commit, in a temporary tree; return its identity.

    The commit is exported with `git archive` (the current worktree is never touched) and that tree's
    scripts/lib/build_release.py builds it as a candidate (signed tag, clean tree, native artifact). The candidate-lock and
    generation-manifest digests of the result must be exactly the ones the policy names: a predecessor that cannot be
    rebuilt to the recorded bytes is not the predecessor.
    """
    state, reason = verdict(policy, repo)
    if state != "ready":
        return state, reason, None
    predecessor = policy["predecessor"]
    work = tempfile.mkdtemp(prefix="telos-predecessor-")
    try:
        export = os.path.join(work, "tree")
        os.mkdir(export)
        archive = subprocess.run(["git", "-C", repo, "archive", "--format=tar", predecessor["sourceCommit"]], capture_output=True, check=False)
        if archive.returncode != 0:
            return "invalid", "git could not export the predecessor commit", None
        tar_path = os.path.join(work, "source.tar")
        with open(tar_path, "wb") as handle:
            handle.write(archive.stdout)
        with tarfile.open(tar_path) as tar:
            tar.extractall(export, filter="data")
        script = builder or os.path.join(export, "scripts", "lib", "build_release.py")
        if not os.path.isfile(script):
            return "not_run", "the predecessor commit has no release builder; it cannot be rebuilt", None
        command = [sys.executable, script, "build", "--repo", repo, "--tag", predecessor["sourceTag"], "--version", predecessor["version"], "--out-dir", out_dir]
        if desktop_dir:
            command += ["--desktop-dir", desktop_dir]
        if tools_dir:
            command += ["--tools-dir", tools_dir]
        built = subprocess.run(command, capture_output=True, text=True, check=False, env=os.environ)
        if built.returncode == 3:
            return "not_run", (built.stderr.strip().splitlines() or ["the predecessor builder is unavailable"])[-1][:240], None
        if built.returncode != 0:
            return "invalid", "the predecessor did not build: " + (built.stderr.strip().splitlines() or ["no diagnostic"])[-1][:240], None
        with open(os.path.join(out_dir, "build-record.json"), "rb") as handle:
            record = json.loads(handle.read())
        with open(os.path.join(out_dir, "candidate", "generation", "generation.json"), "rb") as handle:
            generation_digest = "sha256:" + hashlib.sha256(handle.read()).hexdigest()
        identity = {"path": os.path.join(out_dir, "candidate"), "candidateLockDigest": record["candidateLockDigest"], "generationManifestDigest": generation_digest,
                    "generationId": record["candidateLockDigest"].split(":", 1)[1]}
        if record["candidateLockDigest"] != predecessor["candidateLockDigest"]:
            return "invalid", "the rebuilt candidate lock is not the digest the policy names", identity
        if generation_digest != predecessor["generationManifestDigest"]:
            return "invalid", "the rebuilt generation manifest is not the digest the policy names", identity
        return "ready", "the predecessor was rebuilt to the exact recorded digests", identity
    finally:
        shutil.rmtree(work, ignore_errors=True)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)
    check = sub.add_parser("check")
    check.add_argument("--policy", default=DEFAULT_POLICY)
    check.add_argument("--repo", default=os.path.join(os.path.dirname(DEFAULT_POLICY), ".."))
    rebuild = sub.add_parser("build")
    rebuild.add_argument("--policy", default=DEFAULT_POLICY)
    rebuild.add_argument("--repo", default=os.path.join(os.path.dirname(DEFAULT_POLICY), ".."))
    rebuild.add_argument("--out-dir", required=True)
    rebuild.add_argument("--desktop-dir")
    rebuild.add_argument("--tools-dir")
    rebuild.add_argument("--builder", help=argparse.SUPPRESS)
    args = parser.parse_args(argv)
    try:
        with open(args.policy, "rb") as handle:
            policy = json.loads(handle.read().decode("utf-8"))
    except (OSError, ValueError) as error:
        print(json.dumps({"verdict": "invalid", "reason": f"cannot read the predecessor policy ({error.__class__.__name__})"}))
        return 2
    if args.command == "check":
        state, reason = verdict(policy, args.repo)
        print(json.dumps({"verdict": state, "reason": reason}, sort_keys=True))
    else:
        if not os.path.isabs(args.out_dir):
            print(json.dumps({"verdict": "invalid", "reason": "--out-dir must be absolute"}))
            return 2
        state, reason, identity = build(policy, args.repo, args.out_dir, args.desktop_dir, args.tools_dir, args.builder)
        print(json.dumps({"verdict": state, "reason": reason, "predecessor": identity}, sort_keys=True))
    return {"ready": 0, "not_run": 3, "invalid": 2}[state]


if __name__ == "__main__":
    sys.exit(main())
