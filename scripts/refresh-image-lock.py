#!/usr/bin/env python3
"""Explicitly resolve one image into the reviewed lock (release/images.lock).

    refresh-image-lock.py --section images|toolImages|testImages --key KEY \
        --source REGISTRY/NAME:TAG [--platform linux/amd64] --output PATH

Resolution is a deliberate act that produces a reviewable diff: the registry is
asked (through ``skopeo inspect --raw``, over TLS) for the image index, and the
tag's manifest digest plus the linux/amd64 platform manifest digest are written for
the named key only, atomically. Normal build, install, upgrade and certification
never run this and never move a digest. A mutable ``latest`` source is refused.
Old and new non-secret digests are printed for the reviewer.
"""

import argparse
import datetime
import hashlib
import json
import os
import re
import subprocess
import sys
import tempfile

PLATFORM = "linux/amd64"


def resolve(source):
    reference = f"docker://{source}"
    raw = subprocess.run(["skopeo", "inspect", "--raw", reference], capture_output=True, check=True, timeout=120).stdout
    manifest_digest = "sha256:" + hashlib.sha256(raw).hexdigest()
    reported = subprocess.run(["skopeo", "inspect", "--format", "{{.Digest}}", reference], capture_output=True, text=True, check=True, timeout=120).stdout.strip()
    if reported and reported != manifest_digest:
        raise SystemExit(f"refresh-image-lock: the registry reported {reported} but the manifest hashes to {manifest_digest}")
    document = json.loads(raw)
    media_type = document.get("mediaType", "")
    if "manifests" in document:
        matches = [m for m in document["manifests"]
                   if m.get("platform", {}).get("os") == "linux" and m.get("platform", {}).get("architecture") == "amd64"
                   and "attestation" not in json.dumps(m.get("annotations", {}))]
        if len(matches) != 1:
            raise SystemExit(f"refresh-image-lock: expected exactly one linux/amd64 manifest, found {len(matches)}")
        return manifest_digest, matches[0]["digest"]
    if "config" in document and "layers" in document:
        # A single-platform image: verify it really is linux/amd64 via its config.
        inspected = json.loads(subprocess.run(["skopeo", "inspect", "--override-os", "linux", "--override-arch", "amd64", reference],
                                              capture_output=True, check=True, timeout=120).stdout)
        if inspected.get("Os") != "linux" or inspected.get("Architecture") != "amd64":
            raise SystemExit("refresh-image-lock: the image is not linux/amd64")
        return manifest_digest, manifest_digest
    raise SystemExit(f"refresh-image-lock: unrecognized manifest type {media_type!r}")


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--section", required=True, choices=["images", "toolImages", "testImages"])
    parser.add_argument("--key", required=True)
    parser.add_argument("--source", required=True)
    parser.add_argument("--platform", default=PLATFORM)
    parser.add_argument("--output", required=True)
    args = parser.parse_args(argv)
    if args.platform != PLATFORM:
        raise SystemExit("refresh-image-lock: only linux/amd64 is supported")
    if not re.fullmatch(r"[a-z][a-z0-9-]*", args.key):
        raise SystemExit("refresh-image-lock: --key must be a lowercase identifier")
    match = re.fullmatch(r"([a-z0-9.-]+(?::[0-9]+)?)/([a-z0-9._/-]+):([A-Za-z0-9][A-Za-z0-9._-]*)", args.source)
    if not match:
        raise SystemExit("refresh-image-lock: --source must be a fully qualified registry/name:tag")
    registry, _, tag = match.groups()
    if tag == "latest":
        raise SystemExit("refresh-image-lock: a mutable 'latest' source is refused; choose a version tag")
    manifest_digest, platform_digest = resolve(args.source)
    lock = {"schemaVersion": 2, "images": {}, "toolImages": {}, "testImages": {}}
    if os.path.exists(args.output):
        lock = json.load(open(args.output, encoding="utf-8"))
    old = lock.setdefault(args.section, {}).get(args.key)
    lock[args.section][args.key] = {
        "manifestDigest": manifest_digest, "platforms": {PLATFORM: platform_digest}, "registry": registry,
        "resolvedAt": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), "source": args.source,
    }
    for section in ("images", "toolImages", "testImages"):
        lock[section] = dict(sorted(lock[section].items()))
    text = json.dumps(lock, indent=2, sort_keys=True) + "\n"
    directory = os.path.dirname(os.path.abspath(args.output))
    descriptor, temporary = tempfile.mkstemp(dir=directory, prefix=".images-lock.")
    with os.fdopen(descriptor, "w", encoding="utf-8") as handle:
        handle.write(text)
        handle.flush()
        os.fsync(handle.fileno())
    os.replace(temporary, args.output)
    print(f"{args.section}.{args.key}: {args.source}")
    print(f"  old manifest {old['manifestDigest'] if old else '(none)'}")
    print(f"  new manifest {manifest_digest}")
    print(f"  new {PLATFORM} {platform_digest}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
