#!/usr/bin/env python3
"""Backup manifest: create, validate, compare, and read restic's JSON output.

    backup_manifest.py create  --stage DIR --out FILE --started T --consistency-point T
                               --host-id-sha256 D --release R [--source-commit C]
                               [--candidate-lock-digest D] --tag T ... --migrations FILE
    backup_manifest.py verify  --manifest FILE [--stage DIR]
    backup_manifest.py compare --expected FILE --actual FILE
    backup_manifest.py snapshot-id          (reads ``restic backup --json`` on stdin)
    backup_manifest.py has-tags --snapshots FILE --id ID --tag T ...

The manifest lists every required component with its size and SHA-256. ``verify`` checks
the schema, that all required kinds are present exactly once, that the timestamps are
ordered (the consistency point is not before the start nor after the finish) and, when
given the staging directory, that each file on disk matches. ``compare`` is how a backup
proves the manifest restic returned from the repository is the one that was staged.
Nothing here reads, prints or stores a secret: components are named files and their hashes.
"""

import argparse
import datetime
import hashlib
import json
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import schema_check  # noqa: E402

SCHEMA_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "release", "backup-manifest.schema.json")
COMPONENTS = [
    ("postgres.dump", "postgres-custom-dump"),
    ("grimmory.sql", "mariadb-dump"),
    ("node.env", "node-env"),
    ("node-config.tar", "node-config"),
    ("traefik-acme.tar", "traefik-acme"),
    ("jellyfin-config.tar", "jellyfin-config"),
    ("grimmory-config.tar", "grimmory-config"),
    ("shared-storage.tar", "shared-storage"),
    ("release-capsule.tar", "release-capsule"),
    ("migrations.json", "migration-manifest"),
]
REQUIRED_KINDS = {kind for _, kind in COMPONENTS}


class ManifestError(Exception):
    pass


def load_schema():
    with open(SCHEMA_PATH, encoding="utf-8") as handle:
        schema = json.load(handle)
    schema_check.check_schema(schema)
    return schema


def sha256_file(path):
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def parse_time(text):
    return datetime.datetime.strptime(text, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=datetime.timezone.utc)


def now_utc():
    return datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def validate(document):
    try:
        schema_check.validate(document, load_schema())
    except schema_check.ValidationError as error:
        raise ManifestError(f"the manifest violates its schema: {error}")
    kinds = [component["kind"] for component in document["components"]]
    if sorted(kinds) != sorted(REQUIRED_KINDS):
        missing = sorted(REQUIRED_KINDS - set(kinds))
        extra = sorted(k for k in set(kinds) if kinds.count(k) > 1)
        raise ManifestError(f"components must be exactly the required set (missing {missing}, duplicated {extra})")
    names = [component["name"] for component in document["components"]]
    if len(set(names)) != len(names):
        raise ManifestError("component names must be unique")
    started, point, finished = (parse_time(document[k]) for k in ("startedAt", "consistencyPointAt", "finishedAt"))
    if not started <= point <= finished:
        raise ManifestError("consistencyPointAt must lie between startedAt and finishedAt")
    if parse_time(document["createdAt"]) < started:
        raise ManifestError("createdAt must not precede startedAt")
    if document["excluded"] != ["redis"]:
        raise ManifestError("redis is the one explicit exclusion")
    migrations = document["schema"]["migrations"]
    versions = [m["version"] for m in migrations]
    if versions != sorted(set(versions)):
        raise ManifestError("migrations must be unique and ordered")
    if document["schema"]["current"] != (versions[-1] if versions else 0):
        raise ManifestError("schema.current must be the last recorded migration")


def check_stage(document, stage):
    for component in document["components"]:
        path = os.path.join(stage, component["name"])
        try:
            info = os.lstat(path)
        except OSError:
            raise ManifestError(f"component {component['name']} is missing from the stage")
        if not os.path.isfile(path) or os.path.islink(path):
            raise ManifestError(f"component {component['name']} is not a regular file")
        if info.st_size != component["sizeBytes"] or sha256_file(path) != component["sha256"]:
            raise ManifestError(f"component {component['name']} does not match its recorded size and hash")


def create(args):
    stage = args.stage
    with open(args.migrations, encoding="utf-8") as handle:
        migrations = json.load(handle)
    components = []
    for name, kind in COMPONENTS:
        path = os.path.join(stage, name)
        if not os.path.isfile(path) or os.path.islink(path):
            raise ManifestError(f"required component {name} was not collected")
        components.append({"name": name, "kind": kind, "sizeBytes": os.path.getsize(path), "sha256": sha256_file(path)})
    finished = now_utc()
    document = {
        "formatVersion": 1,
        "kind": "telos-backup",
        "createdAt": finished,
        "startedAt": args.started,
        "finishedAt": finished,
        "consistencyPointAt": args.consistency_point,
        "consistencyMethod": "writers-stopped",
        "source": {
            "hostIdSha256": args.host_id_sha256,
            "sourceCommit": args.source_commit or None,
            "release": args.release,
            "candidateLockDigest": args.candidate_lock_digest or None,
        },
        "schema": {"current": migrations[-1]["version"] if migrations else 0, "migrations": migrations},
        "components": components,
        "excluded": ["redis"],
        "tags": sorted(set(args.tag)),
    }
    validate(document)
    check_stage(document, stage)
    with open(args.out, "w", encoding="utf-8") as handle:
        json.dump(document, handle, indent=2, sort_keys=True)
        handle.write("\n")


def read_json(path):
    with open(path, encoding="utf-8") as handle:
        return json.load(handle)


def snapshot_id(stream):
    """The id from the final summary message of ``restic backup --json``."""
    found = None
    for line in stream.read().splitlines():
        line = line.strip()
        if not line.startswith("{"):
            continue
        try:
            message = json.loads(line)
        except ValueError:
            continue
        if message.get("message_type") == "summary" and re.fullmatch(r"[0-9a-f]{64}", str(message.get("snapshot_id", ""))):
            found = message["snapshot_id"]
    if not found:
        raise ManifestError("restic reported no snapshot id")
    return found


def has_tags(snapshots_file, snapshot, tags):
    entries = read_json(snapshots_file)
    for entry in entries:
        if entry.get("id") == snapshot or entry.get("short_id") == snapshot[:8]:
            missing = sorted(set(tags) - set(entry.get("tags") or []))
            if missing:
                raise ManifestError(f"snapshot {snapshot[:8]} lacks tags {missing}")
            return
    raise ManifestError(f"snapshot {snapshot[:8]} was not found in the repository listing")


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)
    c = sub.add_parser("create")
    c.add_argument("--stage", required=True)
    c.add_argument("--out", required=True)
    c.add_argument("--started", required=True)
    c.add_argument("--consistency-point", required=True)
    c.add_argument("--host-id-sha256", required=True)
    c.add_argument("--release", required=True)
    c.add_argument("--source-commit", default="")
    c.add_argument("--candidate-lock-digest", default="")
    c.add_argument("--tag", action="append", required=True)
    c.add_argument("--migrations", required=True)
    v = sub.add_parser("verify")
    v.add_argument("--manifest", required=True)
    v.add_argument("--stage")
    m = sub.add_parser("compare")
    m.add_argument("--expected", required=True)
    m.add_argument("--actual", required=True)
    sub.add_parser("snapshot-id")
    t = sub.add_parser("has-tags")
    t.add_argument("--snapshots", required=True)
    t.add_argument("--id", required=True)
    t.add_argument("--tag", action="append", required=True)
    args = parser.parse_args(argv)
    try:
        if args.command == "create":
            create(args)
        elif args.command == "verify":
            document = read_json(args.manifest)
            validate(document)
            if args.stage:
                check_stage(document, args.stage)
        elif args.command == "compare":
            expected, actual = read_json(args.expected), read_json(args.actual)
            validate(actual)
            if expected != actual:
                raise ManifestError("the manifest stored in the repository differs from the one that was staged")
        elif args.command == "snapshot-id":
            print(snapshot_id(sys.stdin))
        elif args.command == "has-tags":
            has_tags(args.snapshots, args.id, args.tag)
    except (ManifestError, OSError, ValueError) as error:
        print(f"backup_manifest: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
