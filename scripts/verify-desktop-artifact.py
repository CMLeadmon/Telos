#!/usr/bin/env python3
"""Verify the unsigned Linux desktop artifact without ever executing it.

    verify-desktop-artifact.py --platform linux --artifact PATH --identity PATH
                               [--candidate-lock PATH]

``--artifact`` is the AppImage, or the bundle directory that must contain exactly
one. The identity sidecar is the shared ``artifact-identity`` record (source commit,
version, platform, size, SHA-256, workflow run). Checks, in order:

* the only accepted artifact is one ``.AppImage``; any other extension, a second
  artifact, updater or signing metadata (``latest.json``, ``*.sig``), or a missing
  sidecar rejects;
* the file is non-empty and its bytes match the sidecar's size and SHA-256, and its
  name carries the sidecar's version;
* the payload is a SquashFS reached through the ELF section-header end (the AppImage
  type-2 layout) and is listed/extracted with ``unsquashfs`` -- never by running the
  AppImage -- then scanned for an actual bundled credential file
  (``telos-secrets.json``) and for updater configuration;
* with ``--candidate-lock``, the artifact digest and the sidecar's commit and
  version must agree with the lock. The sidecar and binary are never rewritten to
  embed the lock digest (that would be a hash cycle).

A missing ``unsquashfs`` is ``not_run`` (exit 3): without it the payload cannot be
inspected, and a suffix check alone proves nothing. Exit 1 is a rejected artifact,
2 a usage error.
"""

import argparse
import hashlib
import os
import re
import shutil
import struct
import subprocess
import sys
import tempfile
from pathlib import Path

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "lib"))
import release_graph as rg  # noqa: E402

EXIT_REJECTED, EXIT_USAGE, EXIT_NOT_RUN = 1, 2, 3
FORBIDDEN_PAYLOAD_NAMES = {"telos-secrets.json"}
UPDATER_PATH = re.compile(r"(^|/)(latest\.json|[^/]*updater[^/]*|[^/]*\.sig)$", re.I)
UPDATER_SIDE_FILES = re.compile(r"(^|/)(latest\.json|[^/]*\.sig|[^/]*\.sha256|[^/]*\.zsync)$", re.I)


class Rejected(Exception):
    pass


class NotRun(Exception):
    pass


def require(condition, message):
    if not condition:
        raise Rejected(message)


def sha256_file(path):
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def find_artifact(artifact):
    artifact = Path(artifact)
    if artifact.is_dir():
        entries = sorted(p for p in artifact.rglob("*") if p.is_file())
        names = [p.relative_to(artifact).as_posix() for p in entries]
        side = [n for n in names if UPDATER_SIDE_FILES.search(n) and not n.endswith(".identity.json")]
        require(not side, f"unexpected updater or signing metadata beside the artifact: {side[0] if side else ''}")
        appimages = [p for p in entries if p.name.endswith(".AppImage")]
        others = [n for n in names if not n.endswith(".AppImage") and not n.endswith(".AppImage.identity.json")]
        require(not others, f"unsupported artifact in the desktop bundle: {others[0] if others else ''}")
        require(len(appimages) >= 1, "no AppImage was produced")
        require(len(appimages) == 1, f"duplicate platform artifact: {len(appimages)} AppImages")
        return appimages[0]
    require(artifact.is_file() and not artifact.is_symlink(), "the artifact must be a regular file")
    require(artifact.name.endswith(".AppImage"), f"the only accepted artifact is an .AppImage, not {artifact.suffix or artifact.name}")
    return artifact


def appimage_offset(path):
    """Offset of the SquashFS payload in a type-2 AppImage: the end of the ELF section header table."""
    with open(path, "rb") as handle:
        header = handle.read(64)
        require(header[:4] == b"\x7fELF", "the artifact is not an ELF executable")
        require(header[4] == 2 and header[5] == 1, "the artifact is not a little-endian 64-bit ELF")
        shoff = struct.unpack_from("<Q", header, 0x28)[0]
        shentsize, shnum = struct.unpack_from("<HH", header, 0x3A)
        offset = shoff + shentsize * shnum
        handle.seek(offset)
        magic = handle.read(4)
    require(magic == b"hsqs", "the artifact has no SquashFS payload at the AppImage offset")
    return offset


def inspect_payload(path, offset):
    unsquashfs = shutil.which("unsquashfs")
    if unsquashfs is None:
        raise NotRun("unsquashfs is not installed, so the AppImage payload cannot be inspected (install squashfs-tools)")
    listing = subprocess.run([unsquashfs, "-o", str(offset), "-ll", str(path)], capture_output=True, text=True, timeout=300, check=False)
    require(listing.returncode == 0, "the SquashFS payload could not be listed")
    names = []
    for line in listing.stdout.splitlines():
        match = re.search(r"\s(squashfs-root(?:/\S.*)?)$", line)
        if match:
            names.append(match.group(1).split(" -> ")[0])
    require(names, "the SquashFS payload lists no files")
    for name in names:
        base = name.rsplit("/", 1)[-1]
        require(base not in FORBIDDEN_PAYLOAD_NAMES, f"the bundle contains a credential file: {name}")
        require(not UPDATER_PATH.search(name), f"the bundle carries updater or signing metadata: {name}")
    return names


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--platform", required=True, choices=["linux"])
    parser.add_argument("--artifact", required=True)
    parser.add_argument("--identity")
    parser.add_argument("--candidate-lock")
    args = parser.parse_args(argv)
    try:
        artifact = find_artifact(args.artifact)
        size = artifact.stat().st_size
        require(size > 0, "the artifact is zero-length")
        require(args.identity and Path(args.identity).is_file(), "the artifact identity sidecar is missing")
        identity = rg.load_record("artifact-identity", Path(args.identity).read_bytes(), canonical=False)
        digest = sha256_file(artifact)
        require(identity["sha256"] == digest, "the artifact checksum differs from its identity sidecar")
        require(identity["size"] == size, "the artifact size differs from its identity sidecar")
        require(artifact.name.endswith(".AppImage") and identity["version"] in artifact.name,
                f"the artifact name does not carry the identity version {identity['version']}")
        names = inspect_payload(artifact, appimage_offset(artifact))
        if args.candidate_lock:
            lock = rg.load_record("candidate-lock", Path(args.candidate_lock).read_bytes())
            require(identity["sourceCommit"] == lock["sourceCommit"], "the sidecar source commit differs from the candidate lock")
            require(identity["version"] == lock["version"], "the sidecar version differs from the candidate lock")
            require("sha256:" + digest in lock["artifacts"].values(), "the artifact digest is not in the candidate lock")
    except Rejected as error:
        print(f"verify-desktop-artifact: rejected: {error}", file=sys.stderr)
        return EXIT_REJECTED
    except NotRun as error:
        print(f"verify-desktop-artifact: not_run: {error}", file=sys.stderr)
        return EXIT_NOT_RUN
    except (rg.GraphError, OSError, subprocess.SubprocessError, ValueError, KeyError) as error:
        print(f"verify-desktop-artifact: {type(error).__name__}: {str(error)[:200]}", file=sys.stderr)
        return EXIT_REJECTED
    print(f"verify-desktop-artifact: ok: {artifact.name} sha256:{digest} ({len(names)} payload entries, not executed)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
