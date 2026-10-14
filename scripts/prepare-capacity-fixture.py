#!/usr/bin/env python3
"""Create (and later remove) the disposable accounts and channel a capacity drill needs.

    prepare-capacity-fixture.py prepare --target-file capacity-target.json --lock-digest DIGEST|- \\
        --fixture-manifest FILE --owner-fd N --out-dir DIR [--accounts 100]
    prepare-capacity-fixture.py cleanup --target-file capacity-target.json --lock-digest DIGEST|- \\
        --owner-fd N --out-dir DIR

The target file is an operator-owned, mode-0600 JSON object that names the one node this may run
against:  {"schemaVersion": 1, "purpose": "capacity-test", "candidateLockDigest": "sha256:..."|null,
"origin": "https://...", "hostId": "sha256:...", "sourceAddresses": [...]}  (the last is the load host's
own addresses; the capacity drill reads it, this script only tolerates it).  The script refuses unless purpose is exactly
``capacity-test``, the origin is an HTTPS DNS name that answers over a verified TLS connection, and the
candidate-lock digest matches the one the caller is testing. (The machine identity is checked by the
caller through the fixed node collector; there is no public health field to read it from.)

``prepare`` signs in as the owner, creates a dedicated chat channel, then creates ``--accounts`` members
through the public invite flow, signing each in once to learn its id and out again. It writes, into a
caller-supplied 0700 directory outside the repository, ``load-data.json`` (the fixture ids and every
account's credentials, mode 0600, for the load generator) and ``cleanup.json`` (user and channel ids).
It never prints a password or an invite. ``cleanup`` deletes the users (an account deletion anonymizes
what is public and removes what is private) and the channel with its messages, using only ``cleanup.json``.
The owner's credentials arrive on an open file descriptor, never on a command line.
"""

import argparse
import concurrent.futures
import json
import os
import re
import secrets
import ssl
import stat
import sys
import threading
import time
import urllib.parse
from pathlib import Path

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "lib"))
import browser_node_smoke as smoke  # noqa: E402

WORKERS = 4
PURPOSE = "capacity-test"
ACCOUNT_PREFIX = "capacity-"
EXIT_REFUSED = 2


class Refused(Exception):
    """The request is unsafe or malformed; the message is safe to print."""


def load_target(path, lock_digest):
    try:
        info = os.lstat(path)
    except OSError as error:
        raise Refused(f"cannot read the capacity target ({error.__class__.__name__})")
    if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid() or stat.S_IMODE(info.st_mode) & 0o077:
        raise Refused("the capacity target must be a regular file you own with mode 0600")
    try:
        document = json.loads(Path(path).read_text(encoding="utf-8"))
    except ValueError:
        raise Refused("the capacity target is not JSON")
    required = {"schemaVersion", "purpose", "candidateLockDigest", "origin", "hostId"}
    if not isinstance(document, dict) or not required <= set(document) <= required | {"sourceAddresses"} or document["schemaVersion"] != 1:
        raise Refused("the capacity target must be a closed schemaVersion 1 object")
    if document["purpose"] != PURPOSE:
        raise Refused(f"the capacity target's purpose must be exactly {PURPOSE!r}; refusing to touch a node that was not declared disposable")
    digest = document["candidateLockDigest"]
    wanted = None if lock_digest in (None, "-", "") else lock_digest
    if digest != wanted:
        raise Refused("the capacity target names a different candidate than the one being tested")
    if wanted is not None and not re.fullmatch(r"sha256:[0-9a-f]{64}", wanted):
        raise Refused("the candidate digest is not sha256:<64 hex>")
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", str(document["hostId"])):
        raise Refused("hostId must be sha256:<64 hex>")
    parsed = urllib.parse.urlsplit(str(document["origin"]))
    if parsed.scheme != "https" or not parsed.hostname or parsed.path not in ("", "/") or parsed.query or parsed.username:
        raise Refused("the capacity target's origin must be a bare https origin")
    if re.fullmatch(r"[0-9.]+|\[?[0-9a-fA-F:]+\]?", parsed.hostname):
        raise Refused("the capacity target's origin must use a DNS name, not an IP literal")
    document["origin"] = f"https://{parsed.netloc}"
    return document


def read_owner(fd):
    try:
        with os.fdopen(fd, "r", closefd=False) as handle:
            owner = json.loads(handle.read(65536))
    except (OSError, ValueError):
        raise Refused("the owner credentials must be a JSON object {username, password} on the given file descriptor")
    if not isinstance(owner, dict) or set(owner) != {"username", "password"} or not all(isinstance(v, str) and v for v in owner.values()):
        raise Refused("the owner credentials must be {username, password}")
    return owner


def session_factory(origin, directory, addresses=()):
    """Sessions that cycle through the load host's source addresses: the edge allows 10 sign-ins a minute per address."""
    counter = [0]
    lock = threading.Lock()

    def make():
        with lock:
            counter[0] += 1
            number = counter[0]
        address = addresses[number % len(addresses)] if addresses else None
        return smoke.Session(origin, ssl.create_default_context(), os.path.join(directory, f"jar-{number}.json"), source_address=address)
    return make


def call(session, method, path, expected, **kwargs):
    """json() with a bounded retry for rate limiting (429); the response is never logged."""
    for attempt in range(6):
        status, headers, data = session.request(method, path, **kwargs)
        if status == 429:
            time.sleep(min(30, 2 ** attempt))
            continue
        if status not in expected:
            raise Refused(f"{method} {path.split('?')[0]} answered {status}")
        try:
            return json.loads(data) if data else None
        except ValueError:
            return None
    raise Refused(f"{method} {path.split('?')[0]} was rate limited repeatedly")


def prepare(target, owner, manifest, out_dir, accounts, make_session, sleep=time.sleep):
    origin = target["origin"]
    owner_session = make_session()
    call(owner_session, "POST", "/api/v1/auth/login", (200,), json_body={"username": owner["username"], "password": owner["password"]})
    channel_name = f"capacity-{secrets.token_hex(4)}"
    channel = call(owner_session, "POST", "/api/v1/admin/channels", (200, 201), json_body={"name": channel_name}) or {}
    channel_id = channel.get("id") or channel.get("ID")
    created = {"channelId": channel_id, "userIds": []}
    results = {}
    cleanup_path = os.path.join(out_dir, "cleanup.json")
    lock = threading.Lock()
    owner_lock = threading.Lock()

    def new_invite():
        # Creating an invite is cheap; the expensive work (two password hashes) is the member's accept and sign-in, which run in
        # parallel. One owner session is shared under a lock so the owner signs in once, not once per worker (sign-ins are throttled).
        with owner_lock:
            return call(owner_session, "POST", "/api/v1/auth/invites", (200, 201), json_body={"roleId": "Member"})

    def make_account(number):
        username = f"{ACCOUNT_PREFIX}{number:03d}"
        password = secrets.token_urlsafe(18)
        invite = new_invite()
        token = invite.get("invite_token")
        if not token:
            raise Refused("an invite response carried no token")
        member = make_session()
        call(member, "POST", "/api/v1/auth/invites/accept", (200, 201), json_body={"token": token, "username": username, "password": password})
        call(member, "POST", "/api/v1/auth/login", (200,), json_body={"username": username, "password": password})
        me = call(member, "GET", "/api/v1/auth/me", (200,)) or {}
        user_id = me.get("ID") or me.get("id")
        call(member, "POST", "/api/v1/auth/logout", (200, 204))
        if not user_id:
            raise Refused("/auth/me carried no user id, so the account could not be recorded for cleanup")
        with lock:
            created["userIds"].append(user_id)
            results[number] = {"username": username, "password": password}
            write_private(cleanup_path, created)   # recorded as each account lands, so a crash still leaves a usable manifest
        return number

    credentials = []
    try:
        # Each account costs two password hashes at the gateway; a few workers keep a 100-account fixture to minutes.
        with concurrent.futures.ThreadPoolExecutor(max_workers=WORKERS) as pool:
            for _ in pool.map(make_account, range(1, accounts + 1)):
                pass
        credentials = [results[number] for number in sorted(results)]
        # A member must be able to see and use the channel; prove it before the load starts.
        probe = make_session()
        call(probe, "POST", "/api/v1/auth/login", (200,), json_body=credentials[0])
        channels = call(probe, "GET", "/api/v1/channels", (200,)) or []
        if not any((c.get("name") or c.get("Name")) == channel_name for c in channels):
            raise Refused("the new channel is not visible to a member; the load test would have nothing to write to")
        call(probe, "POST", "/api/v1/auth/logout", (200, 204))
    except BaseException:
        cleanup(target, owner, out_dir, make_session, owner_session=owner_session)
        raise
    data = {"schemaVersion": 1, "fixture": {"chatChannel": channel_name, "video": manifest["video"], "audio": manifest["audio"], "ebook": manifest["ebook"]},
            "accounts": credentials}
    write_private(os.path.join(out_dir, "load-data.json"), data)
    return created


def write_private(path, document):
    temporary = f"{path}.tmp"
    descriptor = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(descriptor, "w", encoding="utf-8") as handle:
        json.dump(document, handle)
    os.replace(temporary, path)


def cleanup(target, owner, out_dir, make_session, owner_session=None):
    path = os.path.join(out_dir, "cleanup.json")
    try:
        created = json.loads(Path(path).read_text(encoding="utf-8"))
    except (OSError, ValueError):
        raise Refused("there is no readable cleanup.json to clean up from")
    session = owner_session
    if session is None:
        session = make_session()
        call(session, "POST", "/api/v1/auth/login", (200,), json_body={"username": owner["username"], "password": owner["password"]})
    failures = 0
    for user_id in created.get("userIds", []):
        try:
            call(session, "DELETE", f"/api/v1/admin/users/{user_id}", (200, 204, 404))
        except Refused:
            failures += 1
    if created.get("channelId"):
        try:
            call(session, "DELETE", f"/api/v1/admin/channels/{created['channelId']}", (200, 204, 404))
        except Refused:
            failures += 1
    if failures:
        raise Refused(f"{failures} fixture object(s) could not be removed; cleanup.json is kept so it can be retried")
    for name in ("cleanup.json", "load-data.json"):
        try:
            os.unlink(os.path.join(out_dir, name))
        except FileNotFoundError:
            pass
    return created


def main(argv=None, make_session_factory=session_factory):   # factory(origin, directory, addresses) -> make()
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("command", choices=("prepare", "cleanup"))
    parser.add_argument("--target-file", required=True)
    parser.add_argument("--lock-digest", required=True)
    parser.add_argument("--fixture-manifest")
    parser.add_argument("--owner-fd", type=int, required=True)
    parser.add_argument("--out-dir", required=True)
    parser.add_argument("--accounts", type=int, default=100)
    parser.add_argument("--source-addresses", default="", help="comma-separated local IPv4 addresses to spread sign-ins across")
    args = parser.parse_args(argv)
    try:
        target = load_target(args.target_file, args.lock_digest)
        owner = read_owner(args.owner_fd)
        out_dir = os.path.abspath(args.out_dir)
        info = os.lstat(out_dir)
        if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid() or stat.S_IMODE(info.st_mode) & 0o077:
            raise Refused("--out-dir must be a directory you own with mode 0700")
        if Path(out_dir) == Path(__file__).resolve().parent.parent or Path(__file__).resolve().parent.parent in Path(out_dir).parents:
            raise Refused("--out-dir must be outside the repository so credentials can never be committed")
        addresses = [a for a in args.source_addresses.split(",") if a]
        for address in addresses:
            if not re.fullmatch(r"(\d{1,3}\.){3}\d{1,3}", address):
                raise Refused(f"{address!r} is not an IPv4 address")
        make_session = make_session_factory(target["origin"], out_dir, addresses)
        if args.command == "prepare":
            if args.accounts < 100 or args.accounts > 500:
                raise Refused("a capacity fixture needs at least 100 accounts (and no more than 500)")
            if not args.fixture_manifest:
                raise Refused("prepare needs --fixture-manifest")
            manifest = smoke.load_manifest(args.fixture_manifest)
            created = prepare(target, owner, manifest, out_dir, args.accounts, make_session)
            print(f"prepare-capacity-fixture: {len(created['userIds'])} accounts and 1 channel ready in {out_dir}")
        else:
            created = cleanup(target, owner, out_dir, make_session)
            print(f"prepare-capacity-fixture: removed {len(created.get('userIds', []))} accounts and the channel")
        return 0
    except Refused as error:
        print(f"prepare-capacity-fixture: {error}", file=sys.stderr)
        return EXIT_REFUSED
    except (smoke.Assertion, smoke.SmokeError) as error:
        print(f"prepare-capacity-fixture: {error}", file=sys.stderr)
        return EXIT_REFUSED


if __name__ == "__main__":
    sys.exit(main())
