#!/usr/bin/env python3
"""Immutable release generations, the activation pointer and the durable operation journal.

Layout (shared contract):

    <root>/generations/<64-hex candidate digest>/   immutable payload, mode 0755, contains generation.json
    <root>/current                                  symlink -> generations/<hex>; the only mutable pointer
    <node>/state/installed-generation.json          receipt: which candidate/manifest is installed (not payload)
    <node>/state/operation.json                     the journal of the upgrade or rollback in progress or last run
    <node>/state/operations/<id>.json               superseded journals, kept for investigation

Nothing here talks to a container runtime, a database or a backup repository. It verifies what is
on disk, swaps the pointer atomically, journals every transition durably (write, fsync, rename,
fsync the directory), and decides -- as a pure function of the journal and two observed facts, the
database schema and whether the node is ready -- what a restarted operation must do next. It never
guesses after a migration: an unexplained schema is `restore`.

    generations.py verify        --root R --id HEX [--no-owner-check]
    generations.py current       --root R
    generations.py activate      --root R --id HEX
    generations.py check-upgrade --root R --from HEX --to HEX --schema N [--no-owner-check]
    generations.py fast-rollback --root R --leaving HEX --target HEX --schema N [--no-owner-check]
    generations.py images        --root R --id HEX      (the digest-addressed images the generation runs, one per line)
    generations.py receipt       --root R --node N --id HEX
    generations.py check-receipt --root R --node N --id HEX
    generations.py journal begin   --node N --kind upgrade|rollback --from HEX|- --to HEX --schema N --host SHA256 [--supersede OPID]
    generations.py journal advance --node N --state STATE [--snapshot ID] [--post-schema N] [--error TEXT]
    generations.py journal show    --node N
    generations.py journal resolve --node N --operation-id ID     (restore-required/failed -> restored, after a restore)
    generations.py plan-recovery   --root R --node N --schema N [--ready true|false]

Exit codes: 0 ok, 1 refused (message on stderr), 2 usage.
"""

import argparse
import json
import os
import re
import secrets
import stat
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import release_graph as rg  # noqa: E402

GENERATION_ID = re.compile(r"[0-9a-f]{64}\Z")
OPERATION_ID = re.compile(r"[0-9a-f]{32}\Z")
JOURNAL_NAME = "operation.json"
RECEIPT_NAME = "installed-generation.json"
TERMINAL = ("complete", "aborted", "rolled-back", "restore-required", "restored", "failed")
BLOCKING = ("restore-required", "failed")   # a new operation needs an explicit acknowledgement of these

# state -> states it may move to; any non-terminal state may also move to restore-required or failed
UPGRADE_FLOW = {
    "started": ("snapshot-verified", "aborted"),
    "snapshot-verified": ("maintenance", "aborted"),
    "maintenance": ("migrating", "aborted"),
    "migrating": ("migrated", "aborted"),
    "migrated": ("activated", "rolled-back"),
    "activated": ("serving", "rolled-back"),
    "serving": ("complete", "rolled-back"),
}
ROLLBACK_FLOW = {
    "started": ("maintenance",),
    "maintenance": ("activated",),
    "activated": ("serving",),
    "serving": ("complete",),
}


class GenerationError(Exception):
    """A refusal; the message is safe to print."""


def fail(message):
    raise GenerationError(message)


def _read(path):
    with open(path, "rb") as handle:
        return handle.read()


def now():
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


# ------------------------------------------------------------------ generations on disk

def check_id(generation_id):
    if not isinstance(generation_id, str) or not GENERATION_ID.match(generation_id):
        fail("a generation id is the 64 hex characters of its candidate digest")
    return generation_id


def id_of(digest):
    if not isinstance(digest, str) or not rg.DIGEST.match(digest):
        fail("a candidate digest is sha256: followed by 64 hex characters")
    return digest.split(":", 1)[1]


def generations_dir(root):
    if not os.path.isabs(root) or os.path.normpath(root) != root:
        fail("the release root must be an absolute, normalized path")
    path = os.path.join(root, "generations")
    if os.path.islink(path) or (os.path.exists(path) and not os.path.isdir(path)):
        fail("the generations directory must be a real directory")
    return path


def generation_path(root, generation_id):
    return os.path.join(generations_dir(root), check_id(generation_id))


def _check_tree(path, require_root_owner):
    """No symlinks, no special files, nothing writable by group or other, root-owned when asked."""
    for directory, names, files in os.walk(path, followlinks=False):
        for name in names + files:
            entry = os.path.join(directory, name)
            info = os.lstat(entry)
            if stat.S_ISLNK(info.st_mode):
                fail(f"{os.path.relpath(entry, path)} is a symlink; generations contain none")
            if not (stat.S_ISREG(info.st_mode) or stat.S_ISDIR(info.st_mode)):
                fail(f"{os.path.relpath(entry, path)} is not a regular file or directory")
            if info.st_mode & 0o022:
                fail(f"{os.path.relpath(entry, path)} is writable by group or other")
            if require_root_owner and info.st_uid != 0:
                fail(f"{os.path.relpath(entry, path)} is not owned by root")


def verify_generation(root, generation_id, require_root_owner=True):
    """Prove a generation directory is exactly what its manifest says; return (manifest, manifest digest)."""
    path = generation_path(root, generation_id)
    try:
        info = os.lstat(path)
    except FileNotFoundError:
        fail(f"generation {generation_id[:12]} is not installed")
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISDIR(info.st_mode):
        fail("a generation must be a real directory, not a link")
    if os.path.realpath(path) != os.path.join(os.path.realpath(root), "generations", generation_id):
        fail("the generation path resolves outside the release root")
    if stat.S_IMODE(info.st_mode) != 0o755:
        fail(f"the generation directory must be mode 0755, found {stat.S_IMODE(info.st_mode):04o}")
    if require_root_owner and info.st_uid != 0:
        fail("the generation directory is not owned by root")
    _check_tree(path, require_root_owner)
    manifest_path = os.path.join(path, "generation.json")
    try:
        data = _read(manifest_path)
    except OSError:
        fail("generation.json is missing")
    try:
        manifest = rg.load_record("generation-manifest", data)
    except rg.GraphError as error:
        fail(f"generation.json is invalid: {error}")
    for item in manifest["artifactIndexSubset"]:
        target = os.path.join(path, item["path"])
        if os.path.islink(target) or not os.path.isfile(target):
            fail(f"{item['path']} is missing from the generation")
        with open(target, "rb") as handle:
            if rg.raw_sha256(handle.read()) != item["sha256"]:
                fail(f"{item['path']} does not match its recorded digest")
    return manifest, rg.digest_bytes(data)


IMAGE_REF = re.compile(r"^\s*image:\s*(\S+)\s*$", re.M)
DIGEST_REF = re.compile(r"[a-z0-9./_-]+@sha256:[0-9a-f]{64}\Z")


def image_refs(directory):
    """Every digest-addressed image a generation (or an unpacked candidate's generation/ tree) runs: images.env plus the third-party references."""
    refs = set()
    with open(os.path.join(directory, "images.env"), encoding="utf-8") as handle:
        for line in handle:
            if line.strip():
                refs.add(line.strip().split("=", 1)[1])
    for name in ("docker-compose.yml", "docker-compose.setup.yml"):
        with open(os.path.join(directory, name), encoding="utf-8") as handle:
            for ref in IMAGE_REF.findall(handle.read()):
                if "${" not in ref:
                    refs.add(ref)
    for ref in refs:
        if not DIGEST_REF.match(ref):
            fail(f"{ref} is not a digest-addressed image reference")
    return sorted(refs)


def current_generation(root):
    """The active generation id, or None when nothing has been activated."""
    link = os.path.join(root, "current")
    try:
        target = os.readlink(link)
    except FileNotFoundError:
        return None
    except OSError:
        fail("current is not a symlink")
    match = re.fullmatch(r"generations/([0-9a-f]{64})", target)
    if not match:
        fail(f"current points at {target!r}, which is not a generation")
    if not os.path.isdir(os.path.join(root, target)):
        fail("current points at a generation that does not exist")
    return match.group(1)


def fsync_directory(path):
    descriptor = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def activate(root, generation_id):
    """Point `current` at an installed generation, atomically; the old pointer stays until the rename."""
    generation_path(root, generation_id)
    if not os.path.isdir(os.path.join(root, "generations", generation_id)):
        fail("cannot activate a generation that is not installed")
    link, temporary = os.path.join(root, "current"), os.path.join(root, f".current.{os.getpid()}.{secrets.token_hex(4)}")
    os.symlink(f"generations/{generation_id}", temporary)
    try:
        os.replace(temporary, link)
    except OSError:
        os.unlink(temporary)
        raise
    fsync_directory(root)


# ------------------------------------------------------------------ compatibility

def check_upgrade(from_manifest, from_id, to_manifest, schema):
    """The target must name the current generation as a supported predecessor and accept the node's schema."""
    if f"sha256:{from_id}" not in to_manifest["predecessorIds"]:
        fail("the target generation does not list the installed generation as a supported predecessor")
    window = to_manifest["migrations"]
    if not window["min"] <= schema <= window["max"]:
        fail(f"the node's schema ({schema}) is outside the target's supported window ({window['min']}..{window['max']})")


def fast_rollback_ok(leaving_manifest, target_manifest, schema):
    """May the previous generation's binaries run against the database as it stands now?

    The generation being left declares the highest schema its predecessor still tolerates
    (rollbackCompatibleThrough); the predecessor declares the lowest it can start on.
    """
    return target_manifest["migrations"]["min"] <= schema <= leaving_manifest["rollbackCompatibleThrough"]


# ------------------------------------------------------------------ durable files

def atomic_replace(path, data, mode=0o600):
    directory = os.path.dirname(path)
    temporary = os.path.join(directory, f".{os.path.basename(path)}.{os.getpid()}.{secrets.token_hex(4)}")
    descriptor = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, mode)
    try:
        with os.fdopen(descriptor, "wb") as handle:
            handle.write(data)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, path)
    except BaseException:
        try:
            os.unlink(temporary)
        except OSError:
            pass
        raise
    fsync_directory(directory)


def state_dir(node):
    if not os.path.isabs(node) or os.path.normpath(node) != node:
        fail("the node path must be an absolute, normalized path")
    path = os.path.join(node, "state")
    if os.path.islink(path) or not os.path.isdir(path):
        fail("the node's state directory is missing or is a symlink")
    return path


def write_receipt(root, node, generation_id, require_root_owner=True):
    manifest, manifest_digest = verify_generation(root, generation_id, require_root_owner)
    receipt = {"schemaVersion": 1, "candidateLockDigest": f"sha256:{generation_id}", "generationManifestDigest": manifest_digest,
               "generationPath": os.path.join(os.path.realpath(root), "generations", generation_id), "installedAt": now()}
    rg.validate_record("installed-generation", receipt)
    atomic_replace(os.path.join(state_dir(node), RECEIPT_NAME), rg.canonical_bytes(receipt))
    # The lock install-candidate kept for this generation becomes the lock services name ("the installed candidate"),
    # replaced together with the receipt so the two can never disagree about what is installed.
    kept = os.path.join(state_dir(node), "candidates", generation_id, "candidate-lock.json")
    if os.path.isfile(kept) and not os.path.islink(kept):
        data = _read(kept)
        if rg.raw_sha256(data) == generation_id:
            atomic_replace(os.path.join(state_dir(node), "current-candidate-lock.json"), data, mode=0o644)
    return receipt


def check_receipt(root, node, generation_id, require_root_owner=True):
    path = os.path.join(state_dir(node), RECEIPT_NAME)
    try:
        receipt = rg.load_record("installed-generation", _read(path))
    except (OSError, rg.GraphError) as error:
        fail(f"the installed-generation receipt is missing or invalid: {error}")
    _, manifest_digest = verify_generation(root, generation_id, require_root_owner)
    if receipt["candidateLockDigest"] != f"sha256:{generation_id}":
        fail("the receipt names a different candidate than the generation")
    if receipt["generationManifestDigest"] != manifest_digest:
        fail("the receipt's manifest digest does not match the generation on disk")
    if receipt["generationPath"] != os.path.join(os.path.realpath(root), "generations", generation_id):
        fail("the receipt's path is not this generation's path")
    return receipt


# ------------------------------------------------------------------ the journal

def journal_path(node):
    return os.path.join(state_dir(node), JOURNAL_NAME)


def read_journal(node):
    path = journal_path(node)
    try:
        data = _read(path)
    except FileNotFoundError:
        return None
    try:
        return rg.load_record("operation-journal", data)
    except rg.GraphError as error:
        fail(f"the operation journal is invalid ({error}); nothing will guess past it -- investigate before any upgrade or rollback")


def write_journal(node, record):
    rg.validate_record("operation-journal", record)
    atomic_replace(journal_path(node), rg.canonical_bytes(record))


def begin(node, kind, from_id, to_id, schema, host, supersede=None):
    existing = read_journal(node)
    if existing is not None:
        if existing["state"] not in TERMINAL:
            fail(f"operation {existing['operationId'][:8]} ({existing['kind']}, {existing['state']}) is unfinished; recover it first")
        if existing["state"] in BLOCKING and supersede != existing["operationId"]:
            fail(f"operation {existing['operationId'][:8]} ended {existing['state']}; acknowledge it with --supersede {existing['operationId']} after reading the journal")
        archive = os.path.join(state_dir(node), "operations")
        os.makedirs(archive, mode=0o700, exist_ok=True)
        atomic_replace(os.path.join(archive, f"{existing['operationId']}.json"), rg.canonical_bytes(existing))
    stamp = now()
    record = {"schemaVersion": 1, "operationId": secrets.token_hex(16), "kind": kind, "hostIdSha256": host,
              "fromGeneration": None if from_id in (None, "-") else check_id(from_id), "toGeneration": check_id(to_id),
              "snapshotId": None, "preSchema": schema, "postSchema": None, "state": "started",
              "startedAt": stamp, "updatedAt": stamp, "lastError": None}
    write_journal(node, record)
    return record


def resolve(node, operation_id, state="restored"):
    """Close a blocking operation (restore-required / failed) once its snapshot has been restored."""
    record = read_journal(node)
    if record is None or record["operationId"] != operation_id:
        fail("that operation is not the node's last operation")
    if record["state"] not in BLOCKING:
        fail(f"only an operation that ended {' or '.join(BLOCKING)} can be resolved by a restore; this one is {record['state']}")
    if state != "restored":
        fail("a blocked operation can only be resolved as restored")
    record.update(state=state, updatedAt=now())
    write_journal(node, record)
    return record


def advance(node, state, snapshot=None, post_schema=None, error=None):
    record = read_journal(node)
    if record is None:
        fail("there is no operation to advance")
    current = record["state"]
    if current in TERMINAL:
        fail(f"the operation already ended {current}")
    flow = UPGRADE_FLOW if record["kind"] == "upgrade" else ROLLBACK_FLOW
    allowed = tuple(flow.get(current, ())) + ("restore-required", "failed")
    if state not in allowed:
        fail(f"a {record['kind']} cannot move from {current} to {state}")
    if snapshot is not None:
        record["snapshotId"] = snapshot
    if post_schema is not None:
        record["postSchema"] = post_schema
    record.update(state=state, updatedAt=now(), lastError=(error[:300] if error else record["lastError"]))
    write_journal(node, record)
    return record


# ------------------------------------------------------------------ recovery planning

def plan_recovery(journal, schema, ready=None, manifests=None):
    """What must a restarted operation do? A pure function of the journal, the observed schema and readiness.

    Actions: none | abort | continue | verify | rollback | restore.
      abort     nothing durable changed; make the generation we left active again and start it
      continue  the schema already moved to the target's; activate the target and start it
      verify    the target is active; check readiness, then ask again with --ready
      rollback  fast rollback to the generation we left (its binaries accept the migrated schema)
      restore   the schema is not one this operation explains, or the old binaries cannot run on it
    """
    def result(action, reason):
        return {"action": action, "reason": reason, "operationId": journal["operationId"] if journal else None}

    if journal is None:
        return result("none", "no operation has run on this node")
    state, kind = journal["state"], journal["kind"]
    if state in TERMINAL:
        return result("none", f"the last operation ended {state}")
    pre, post = journal["preSchema"], journal["postSchema"]
    if kind == "rollback":
        if state in ("started", "maintenance"):
            return result("abort", "the rollback had not activated anything; the generation being left is still the one to run")
        return result("verify" if ready is None else "none" if ready else "restore",
                      "the rollback target is active; readiness decides" if ready is None else "ready" if ready else "the rolled-back node is not ready and no further automatic step exists")
    if state in ("started", "snapshot-verified", "maintenance"):
        return result("abort", f"interrupted in {state}, before any migration began")
    if state == "migrating":
        if schema == pre:
            return result("abort", "interrupted during migration but the schema is unchanged")
        if post is not None and schema == post:
            return result("continue", "the migrations finished; only activation is left")
        return result("restore", f"interrupted during migration and the schema ({schema}) is neither {pre} nor the target's; the database is in an unexplained state")
    if state == "migrated":
        if post is not None and schema == post:
            return result("continue", "migrated; activate the target")
        return result("restore", f"the journal says migrated but the schema is {schema}, not {post}")
    # activated or serving
    if ready is None:
        return result("verify", f"the target is active ({state}); readiness decides the branch")
    if ready:
        return result("none", "the target is active and ready; mark the operation complete")
    previous, following = (manifests or {}).get("previous"), (manifests or {}).get("next")
    if previous is not None and following is not None and fast_rollback_ok(following, previous, schema):
        return result("rollback", "the target is not ready and the previous binaries accept the current schema")
    return result("restore", "the target is not ready and the previous binaries cannot run on the migrated schema; restore the recorded snapshot")


# ------------------------------------------------------------------ command line

def parse_bool(text):
    if text not in ("true", "false"):
        raise argparse.ArgumentTypeError("expected true or false")
    return text == "true"


def build_parser():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)

    def root_args(p, ids=("id",)):
        p.add_argument("--root", required=True)
        for name in ids:
            p.add_argument(f"--{name}", required=True)
        p.add_argument("--no-owner-check", action="store_true", help="skip the root-ownership requirement (tests)")
    root_args(sub.add_parser("verify"))
    p = sub.add_parser("current"); p.add_argument("--root", required=True)
    p = sub.add_parser("activate"); p.add_argument("--root", required=True); p.add_argument("--id", required=True)
    p = sub.add_parser("check-upgrade"); root_args(p, ("from", "to")); p.add_argument("--schema", type=int, required=True)
    p = sub.add_parser("fast-rollback"); root_args(p, ("leaving", "target")); p.add_argument("--schema", type=int, required=True)
    root_args(sub.add_parser("images"))
    p = sub.add_parser("receipt"); root_args(p); p.add_argument("--node", required=True)
    p = sub.add_parser("check-receipt"); root_args(p); p.add_argument("--node", required=True)
    journal = sub.add_parser("journal").add_subparsers(dest="journal_command", required=True)
    p = journal.add_parser("begin")
    p.add_argument("--node", required=True); p.add_argument("--kind", choices=("upgrade", "rollback"), required=True)
    p.add_argument("--from", dest="from_id", required=True); p.add_argument("--to", required=True)
    p.add_argument("--schema", type=int, required=True); p.add_argument("--host", required=True); p.add_argument("--supersede")
    p = journal.add_parser("advance")
    p.add_argument("--node", required=True); p.add_argument("--state", required=True)
    p.add_argument("--snapshot"); p.add_argument("--post-schema", type=int); p.add_argument("--error")
    p = journal.add_parser("show"); p.add_argument("--node", required=True)
    p = journal.add_parser("resolve"); p.add_argument("--node", required=True); p.add_argument("--operation-id", required=True)
    p = sub.add_parser("plan-recovery")
    p.add_argument("--root", required=True); p.add_argument("--node", required=True)
    p.add_argument("--schema", type=int, required=True); p.add_argument("--ready", type=parse_bool)
    p.add_argument("--no-owner-check", action="store_true")
    return parser


def run(args):
    owner = not getattr(args, "no_owner_check", False)
    if args.command == "verify":
        manifest, digest = verify_generation(args.root, args.id, owner)
        return {"id": args.id, "manifestDigest": digest, "version": manifest["version"], "migrations": manifest["migrations"]}
    if args.command == "current":
        return {"current": current_generation(args.root)}
    if args.command == "activate":
        activate(args.root, args.id)
        return {"current": current_generation(args.root)}
    if args.command == "check-upgrade":
        from_id = getattr(args, "from")
        from_manifest, _ = verify_generation(args.root, from_id, owner)
        to_manifest, _ = verify_generation(args.root, args.to, owner)
        check_upgrade(from_manifest, from_id, to_manifest, args.schema)
        return {"ok": True}
    if args.command == "fast-rollback":
        leaving, _ = verify_generation(args.root, args.leaving, owner)
        target, _ = verify_generation(args.root, args.target, owner)
        return {"ok": fast_rollback_ok(leaving, target, args.schema)}
    if args.command == "images":
        verify_generation(args.root, args.id, owner)
        return {"images": image_refs(generation_path(args.root, args.id))}
    if args.command == "receipt":
        return write_receipt(args.root, args.node, args.id, owner)
    if args.command == "check-receipt":
        return check_receipt(args.root, args.node, args.id, owner)
    if args.command == "journal":
        if args.journal_command == "begin":
            return begin(args.node, args.kind, args.from_id, args.to, args.schema, args.host, args.supersede)
        if args.journal_command == "advance":
            return advance(args.node, args.state, args.snapshot, args.post_schema, args.error)
        if args.journal_command == "resolve":
            return resolve(args.node, args.operation_id)
        return read_journal(args.node) or {}
    if args.command == "plan-recovery":
        journal = read_journal(args.node)
        manifests = None
        if journal and journal["fromGeneration"]:
            try:
                manifests = {"previous": verify_generation(args.root, journal["fromGeneration"], owner)[0],
                             "next": verify_generation(args.root, journal["toGeneration"], owner)[0]}
            except GenerationError:
                manifests = None
        return plan_recovery(journal, args.schema, args.ready, manifests)
    raise AssertionError(args.command)


def main(argv=None):
    parser = build_parser()
    args = parser.parse_args(argv)
    try:
        document = run(args)
    except (GenerationError, rg.GraphError, OSError) as error:
        print(f"generations: {error}", file=sys.stderr)
        return 1
    json.dump(document, sys.stdout, sort_keys=True)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
