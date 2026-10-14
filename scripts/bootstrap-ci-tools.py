#!/usr/bin/env python3
"""Fetch and verify the supply-chain scanners named in ci/tools.lock.

    bootstrap-ci-tools.py --tool syft|trivy|gitleaks --cache-dir DIR [--lock ci/tools.lock] [--platform linux-amd64]
    bootstrap-ci-tools.py --tool NAME --verify-system-tool PATH [--lock ...]

A tool is a release archive pinned by version, platform, HTTPS URL and SHA-256, plus the SHA-256 of the one
binary inside it. The archive is downloaded into a private (0700, caller-owned) cache directory, its digest
is checked BEFORE anything is extracted or run, only the named member is extracted (never a path from the
archive), and the binary's own digest is checked again on every use. Nothing falls back to $PATH: a system
copy is accepted only through --verify-system-tool, and only if its bytes and its --version match the lock.
Prints the verified binary's absolute path on stdout. Exit codes: 0 verified, 1 refused, 2 usage.
"""

import argparse
import hashlib
import io
import json
import os
import re
import shutil
import stat
import subprocess
import sys
import tarfile
import urllib.parse
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DEFAULT_LOCK = ROOT / "ci" / "tools.lock"
ALLOWED_HOSTS = {"github.com"}
MAX_ARCHIVE_BYTES = 200 * 1024 * 1024
MAX_BINARY_BYTES = 400 * 1024 * 1024
HEX64 = re.compile(r"[0-9a-f]{64}\Z")
NAME = re.compile(r"[a-z][a-z0-9-]*\Z")


class Refused(Exception):
    """The lock, the download or the cache is unsafe; the message is safe to print."""


def load_lock(path):
    try:
        lock = json.loads(Path(path).read_text(encoding="utf-8"))
    except (OSError, ValueError) as error:
        raise Refused(f"cannot read the tool lock ({error.__class__.__name__})")
    if not isinstance(lock, dict) or lock.get("schemaVersion") != 2 or not isinstance(lock.get("tools"), dict):
        raise Refused("ci/tools.lock must be a schemaVersion 2 object with a tools map")
    for name, tool in lock["tools"].items():
        if not NAME.match(name) or set(tool) != {"version", "platforms"} or not re.fullmatch(r"\d+\.\d+\.\d+", str(tool["version"])):
            raise Refused(f"tool {name!r} is malformed")
        for platform, entry in tool["platforms"].items():
            if set(entry) != {"url", "sha256", "member", "binarySha256"} or not HEX64.match(str(entry["sha256"])) or not HEX64.match(str(entry["binarySha256"])):
                raise Refused(f"{name}/{platform}: url, sha256, member and binarySha256 are all required, as lowercase SHA-256 hex")
            parsed = urllib.parse.urlsplit(entry["url"])
            if parsed.scheme != "https" or parsed.hostname not in ALLOWED_HOSTS or parsed.username or parsed.query or parsed.fragment:
                raise Refused(f"{name}/{platform}: the URL must be a plain https URL on {sorted(ALLOWED_HOSTS)}")
            if "/" in entry["member"] or entry["member"] in ("", ".", ".."):
                raise Refused(f"{name}/{platform}: the member must be a bare file name")
            if tool["version"] not in entry["url"]:
                raise Refused(f"{name}/{platform}: the URL does not name version {tool['version']}")
    return lock


def sha256_of(data):
    return hashlib.sha256(data).hexdigest()


def check_cache(cache_dir):
    path = Path(cache_dir)
    if not path.is_absolute():
        raise Refused("--cache-dir must be an absolute path")
    if ROOT in path.parents or path == ROOT:
        raise Refused("--cache-dir must be outside the repository")
    path.mkdir(mode=0o700, parents=True, exist_ok=True)
    info = os.lstat(path)
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid() or stat.S_IMODE(info.st_mode) & 0o077:
        raise Refused("--cache-dir must be a real directory you own with mode 0700")
    return path


def fetch(url, limit=MAX_ARCHIVE_BYTES):
    request = urllib.request.Request(url, headers={"User-Agent": "telos-bootstrap-ci-tools/1"})
    with urllib.request.urlopen(request, timeout=120) as response:   # noqa: S310 -- https and host were validated against the lock
        data = response.read(limit + 1)
    if len(data) > limit:
        raise Refused("the download is larger than the allowed archive size")
    return data


def extract_member(archive, member):
    """The bytes of exactly one named regular file; nothing else in the archive is read or written."""
    try:
        with tarfile.open(fileobj=io.BytesIO(archive), mode="r:*") as tar:
            matches = [m for m in tar.getmembers() if m.name in (member, f"./{member}")]
            if len(matches) != 1 or not matches[0].isfile():
                raise Refused(f"the archive does not contain exactly one regular file named {member!r}")
            if matches[0].size > MAX_BINARY_BYTES:
                raise Refused("the binary is larger than the allowed size")
            return tar.extractfile(matches[0]).read()
    except tarfile.TarError as error:
        raise Refused(f"the archive is unreadable ({error.__class__.__name__})")


def bootstrap(lock, tool, cache_dir, platform="linux-amd64", downloader=fetch):
    if tool not in lock["tools"]:
        raise Refused(f"{tool!r} is not a locked tool")
    entry = lock["tools"][tool]["platforms"].get(platform)
    if entry is None:
        raise Refused(f"{tool} has no locked build for {platform}")
    version = lock["tools"][tool]["version"]
    target_dir = check_cache(cache_dir) / tool / version
    binary = target_dir / entry["member"]
    if binary.is_file() and not binary.is_symlink():
        if sha256_of(binary.read_bytes()) == entry["binarySha256"]:
            return str(binary)
        binary.unlink()   # a cached copy that does not match the lock is never run
    archive = downloader(entry["url"])
    if sha256_of(archive) != entry["sha256"]:
        raise Refused(f"{tool}: the downloaded archive does not match the locked SHA-256; nothing was extracted")
    content = extract_member(archive, entry["member"])
    if sha256_of(content) != entry["binarySha256"]:
        raise Refused(f"{tool}: the extracted binary does not match the locked SHA-256")
    target_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
    temporary = binary.with_suffix(".part")
    descriptor = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o700)
    with os.fdopen(descriptor, "wb") as handle:
        handle.write(content)
    os.replace(temporary, binary)
    os.chmod(binary, 0o755)
    return str(binary)


def verify_system_tool(lock, tool, path, platform="linux-amd64", run=subprocess.run):
    if tool not in lock["tools"]:
        raise Refused(f"{tool!r} is not a locked tool")
    entry = lock["tools"][tool]["platforms"].get(platform)
    version = lock["tools"][tool]["version"]
    candidate = Path(path)
    if not candidate.is_absolute() or candidate.is_symlink() or not candidate.is_file():
        raise Refused("--verify-system-tool needs the absolute path of a regular file")
    if sha256_of(candidate.read_bytes()) != entry["binarySha256"]:
        raise Refused(f"{path} is not the locked {tool} {version} binary (its SHA-256 differs)")
    completed = run([str(candidate), "--version"] if tool != "gitleaks" else [str(candidate), "version"], capture_output=True, text=True, timeout=30, check=False)
    if version not in (completed.stdout + completed.stderr):
        raise Refused(f"{path} does not report version {version}")
    return str(candidate)


def main(argv=None, downloader=fetch):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--tool", required=True)
    parser.add_argument("--cache-dir")
    parser.add_argument("--lock", default=str(DEFAULT_LOCK))
    parser.add_argument("--platform", default="linux-amd64")
    parser.add_argument("--verify-system-tool")
    args = parser.parse_args(argv)
    try:
        lock = load_lock(args.lock)
        if args.verify_system_tool:
            print(verify_system_tool(lock, args.tool, args.verify_system_tool, args.platform))
        elif args.cache_dir:
            print(bootstrap(lock, args.tool, args.cache_dir, args.platform, downloader))
        else:
            print("bootstrap-ci-tools: pass --cache-dir (download and verify) or --verify-system-tool PATH", file=sys.stderr)
            return 2
    except Refused as error:
        print(f"bootstrap-ci-tools: {error}", file=sys.stderr)
        return 1
    except OSError as error:
        print(f"bootstrap-ci-tools: {error.__class__.__name__}: {str(error)[:160]}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
