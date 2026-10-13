#!/usr/bin/env python3
"""Write the shared artifact-identity sidecar for a built desktop artifact.

    write-artifact-identity.py --artifact PATH --source-commit SHA --version X.Y.Z-beta.N \
        --run-id N --run-attempt N --out PATH

The sidecar binds the artifact's exact bytes (size and SHA-256) to the source
commit, version and the workflow run that built it. It is written beside the
artifact and never embedded in it, so the artifact digest cannot depend on a
candidate lock that is derived from that digest (a hash cycle).
"""

import argparse
import hashlib
import os
import re
import sys
from pathlib import Path

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "lib"))
import release_graph as rg  # noqa: E402


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--artifact", required=True)
    parser.add_argument("--source-commit", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--run-id", required=True, type=int)
    parser.add_argument("--run-attempt", required=True, type=int)
    parser.add_argument("--out", required=True)
    args = parser.parse_args(argv)
    artifact = Path(args.artifact)
    if not artifact.is_file() or artifact.is_symlink() or not artifact.name.endswith(".AppImage"):
        print("write-artifact-identity: --artifact must be a regular .AppImage file", file=sys.stderr)
        return 2
    if args.version not in artifact.name:
        print(f"write-artifact-identity: the artifact name does not carry version {args.version}", file=sys.stderr)
        return 2
    if not re.fullmatch(r"[0-9a-f]{40}", args.source_commit):
        print("write-artifact-identity: --source-commit must be a 40-hex commit", file=sys.stderr)
        return 2
    digest = hashlib.sha256()
    with open(artifact, "rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b""):
            digest.update(chunk)
    identity = {
        "schemaVersion": 1, "platform": "linux-x86_64", "sourceCommit": args.source_commit, "version": args.version,
        "size": artifact.stat().st_size, "sha256": digest.hexdigest(),
        "workflowRunId": args.run_id, "workflowRunAttempt": args.run_attempt,
    }
    try:
        rg.validate_record("artifact-identity", identity)
        rg.write_atomic_exclusive(args.out, rg.canonical_bytes(identity))
    except rg.GraphError as error:
        print(f"write-artifact-identity: {error}", file=sys.stderr)
        return 1
    print(f"write-artifact-identity: {args.out} binds {artifact.name} sha256:{identity['sha256']}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
