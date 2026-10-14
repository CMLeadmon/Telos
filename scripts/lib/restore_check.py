#!/usr/bin/env python3
"""Pre-flight checks for a restore: everything that can be proved before a service is touched.

    restore_check.py plan [--mode separate-host|rollback] --data DIR --target-machine-id-sha256 D --migrations DIR
                          [--candidate-lock-digest D | --rehearsal]
                          --free TMP=BYTES --free STORAGE=BYTES [--free VOLUMES=BYTES]
    restore_check.py tar  ARCHIVE [ARCHIVE ...]

``plan`` takes the directory a snapshot was restored into and refuses unless:
  * the manifest is valid and every component matches its recorded size and SHA-256;
  * the host being restored to is NOT the host that produced the backup (a restore on the
    same machine proves nothing about recovery) -- except `--mode rollback`, which is the
    reverse: it undoes a failed upgrade on the host that made the pre-upgrade snapshot;
  * the candidate lock matches the one the backup was made under (a rehearsal states that it
    is skipping this and says so);
  * the backup's schema is not newer than the release being restored to, and every
    migration the backup recorded still has the checksum the release carries (drift means
    the history is not a prefix of this release's);
  * each filesystem the restore writes to has room (components x 2, plus headroom).

``tar`` rejects an archive before anything is extracted: absolute or ``..`` names, device,
FIFO and other special files, hard links that leave the tree, symlinks whose target is
absolute or escapes the extraction root, and overlong or NUL-containing names. These checks
read headers only and never extract.

Output is one human-readable refusal reason per line on stderr; exit 0 means every check
passed, 1 a refusal, 2 usage. Nothing here prints a secret: it handles hashes and sizes.
"""

import argparse
import hashlib
import json
import os
import re
import sys
import tarfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import backup_manifest as bm  # noqa: E402

HEADROOM_BYTES = 256 * 1024 * 1024
MAX_NAME = 4096
MIGRATION_FILE = re.compile(r"^(\d{4})_[A-Za-z0-9_.-]+\.sql$")


class Refusal(Exception):
    pass


def target_migrations(directory):
    """version -> sha256 of every migration file the target release carries."""
    found = {}
    for name in sorted(os.listdir(directory)):
        match = MIGRATION_FILE.match(name)
        if not match:
            continue
        with open(os.path.join(directory, name), "rb") as handle:
            found[int(match.group(1))] = hashlib.sha256(handle.read()).hexdigest()
    versions = sorted(found)
    if versions != list(range(1, len(versions) + 1)):
        raise Refusal("the release's migrations are not contiguous")
    return found


def plan(args):
    problems = []
    manifest_path = os.path.join(args.data, "manifest.json")
    try:
        document = bm.read_json(manifest_path)
        bm.validate(document)
        bm.check_stage(document, args.data)
    except (bm.ManifestError, OSError, ValueError) as error:
        raise Refusal(f"the backup manifest is not trustworthy: {error}")

    same_host = document["source"]["hostIdSha256"] == args.target_machine_id_sha256
    if args.mode == "rollback":
        if not same_host:
            problems.append("a rollback restore runs on the machine that made the backup; this is a different machine")
    elif same_host:
        problems.append("this is the machine that made the backup; a separate-host restore needs a different machine")

    recorded = document["source"]["candidateLockDigest"]
    if args.rehearsal:
        print("restore_check: REHEARSAL -- candidate binding is not checked; this restore is not evidence", file=sys.stderr)
    elif not args.candidate_lock_digest:
        problems.append("a candidate lock is required (or --rehearsal, which cannot produce evidence)")
    elif recorded != args.candidate_lock_digest:
        problems.append("the backup was made under a different candidate than the one being restored")

    try:
        known = target_migrations(args.migrations)
    except (Refusal, OSError) as error:
        raise Refusal(f"cannot read the target release's migrations: {error}")
    backup_migrations = document["schema"]["migrations"]
    if document["schema"]["current"] > len(known):
        problems.append(f"the backup's schema (migration {document['schema']['current']}) is newer than this release ({len(known)})")
    for migration in backup_migrations:
        expected = known.get(migration["version"])
        if expected is None:
            continue  # already reported as newer
        if expected != migration["checksum"]:
            problems.append(f"migration {migration['version']:04d} differs from the one this release carries (history drift)")

    required = sum(component["sizeBytes"] for component in document["components"])
    for entry in args.free:
        label, _, free = entry.partition("=")
        if not free.isdigit():
            raise Refusal(f"--free expects LABEL=BYTES, got {entry!r}")
        if int(free) < required * 2 + HEADROOM_BYTES:
            problems.append(f"not enough free space for {label}: {int(free) // (1 << 20)} MiB free, "
                            f"{(required * 2 + HEADROOM_BYTES) // (1 << 20)} MiB needed")
    if problems:
        raise Refusal("\n".join(problems))
    return document


def check_tar(path):
    problems = []
    try:
        archive = tarfile.open(path, "r:*")
    except (tarfile.TarError, OSError) as error:
        return [f"{os.path.basename(path)}: not a readable archive ({error})"]
    with archive:
        for member in archive:
            name = member.name
            if len(name) > MAX_NAME or "\0" in name:
                problems.append(f"{path}: a member name is overlong or contains NUL")
                continue
            normalized = os.path.normpath(name)
            if os.path.isabs(name) or normalized.startswith("..") or ".." in name.split("/"):
                problems.append(f"{path}: member {name!r} is absolute or escapes the extraction root")
                continue
            if member.isdev() or member.isfifo() or member.ischr() or member.isblk():
                problems.append(f"{path}: member {name!r} is a device or FIFO")
                continue
            if member.issym():
                target = member.linkname
                resolved = os.path.normpath(os.path.join(os.path.dirname(normalized), target))
                if os.path.isabs(target) or resolved.startswith(".."):
                    problems.append(f"{path}: symlink {name!r} -> {target!r} leaves the extraction root")
            elif member.islnk():
                target = os.path.normpath(member.linkname)
                if os.path.isabs(member.linkname) or target.startswith(".."):
                    problems.append(f"{path}: hard link {name!r} -> {member.linkname!r} leaves the extraction root")
            elif not (member.isfile() or member.isdir()):
                problems.append(f"{path}: member {name!r} has an unsupported type")
    return problems


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)
    p = sub.add_parser("plan")
    p.add_argument("--mode", choices=("separate-host", "rollback"), default="separate-host")
    p.add_argument("--data", required=True)
    p.add_argument("--target-machine-id-sha256", required=True)
    p.add_argument("--migrations", required=True)
    p.add_argument("--candidate-lock-digest", default="")
    p.add_argument("--rehearsal", action="store_true")
    p.add_argument("--free", action="append", default=[])
    t = sub.add_parser("tar")
    t.add_argument("archives", nargs="+")
    args = parser.parse_args(argv)
    try:
        if args.command == "plan":
            plan(args)
        else:
            problems = [item for path in args.archives for item in check_tar(path)]
            if problems:
                raise Refusal("\n".join(problems))
    except Refusal as error:
        for line in str(error).splitlines():
            print(f"restore: refused: {line}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
