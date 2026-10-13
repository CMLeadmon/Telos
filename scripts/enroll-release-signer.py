#!/usr/bin/env python3
"""Enroll and validate the OpenSSH public keys that anchor release trust.

Only public keys are ever read. A key is accepted only after its SHA-256
fingerprint is confirmed by the operator, and each key is bound to the
signature namespaces of its purpose. ``--verify-files`` exits 3 (not_run) while
no reviewed key is enrolled, and 1 for any malformed, duplicate or fabricated
entry.
"""

import argparse
import base64
import os
import re
import subprocess
import sys
import tempfile
from pathlib import Path

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "lib"))
from signing import NAMESPACES, PRINCIPAL  # noqa: E402

DEFAULT_RELEASE_DIR = Path(__file__).resolve().parents[1] / "release"
PURPOSES = {
    "tag": ("telos-tag-signers", ["git"]),
    "evidence": ("evidence-signers", [
        NAMESPACES["evidence"], NAMESPACES["certification"],
        NAMESPACES["review"], NAMESPACES["guided"],
    ]),
}
KEY_TYPES = ("ssh-ed25519", "sk-ssh-ed25519@openssh.com")
FINGERPRINT = re.compile(r"SHA256:[A-Za-z0-9+/]{43}\Z")
ENTRY = re.compile(r'(\S+) namespaces="([^"]+)" (\S+) (\S+)\Z')
EXIT_NOT_RUN = 3


class EnrollError(Exception):
    pass


def fail(message):
    raise EnrollError(message)


def parse_public_key(text):
    """Return (key_type, base64_blob) from exactly one public key line."""
    if "-----BEGIN" in text or "PRIVATE KEY" in text:
        fail("refusing a file that looks like private key material")
    lines = text.split("\n")
    if lines and lines[-1] == "":
        lines.pop()
    if len(lines) != 1:
        fail("expected exactly one public key line")
    line = lines[0]
    if any(ord(ch) < 32 or ord(ch) == 127 for ch in line):
        fail("public key line contains control characters")
    if "..." in line or "…" in line:
        fail("public key contains an ellipsis; it is fabricated or truncated")
    parts = line.split(" ", 2)
    if len(parts) < 2:
        fail("public key line is malformed")
    key_type, blob = parts[0], parts[1]
    if key_type not in KEY_TYPES:
        fail(f"unsupported key type {key_type!r}; use ssh-ed25519 or a hardware-backed sk-ssh-ed25519 key")
    try:
        decoded = base64.b64decode(blob, validate=True)
    except ValueError:
        fail("public key body is not valid base64")
    length = int.from_bytes(decoded[:4], "big")
    if decoded[4:4 + length].decode("ascii", "replace") != key_type:
        fail("public key body does not match its declared type")
    return key_type, blob


def fingerprint_of(key_type, blob):
    with tempfile.TemporaryDirectory() as scratch:
        path = Path(scratch) / "key.pub"
        path.write_text(f"{key_type} {blob}\n", encoding="ascii")
        done = subprocess.run(["ssh-keygen", "-l", "-f", str(path)], capture_output=True, text=True, check=False)
    match = re.search(r"SHA256:[A-Za-z0-9+/]{43}", done.stdout)
    if done.returncode != 0 or match is None:
        fail("ssh-keygen rejected the public key")
    return match.group(0)


def read_entries(path):
    entries = []
    if not path.exists():
        return entries
    for number, raw in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        if not raw.strip() or raw.lstrip().startswith("#"):
            continue
        match = ENTRY.match(raw)
        if match is None:
            fail(f"{path.name}:{number}: not an enrolled-signer line")
        principal, namespaces, key_type, blob = match.groups()
        if not PRINCIPAL.match(principal):
            fail(f"{path.name}:{number}: invalid principal")
        parse_public_key(f"{key_type} {blob}\n")
        entries.append((principal, namespaces.split(","), key_type, blob))
    return entries


def check_unique(entries, name):
    principals = [entry[0] for entry in entries]
    blobs = [entry[3] for entry in entries]
    if len(set(principals)) != len(principals):
        fail(f"{name}: duplicate principal")
    if len(set(blobs)) != len(blobs):
        fail(f"{name}: duplicate key material")


def render(entries, header):
    lines = [header]
    for principal, namespaces, key_type, blob in entries:
        lines.append(f'{principal} namespaces="{",".join(namespaces)}" {key_type} {blob}')
    return ("\n".join(lines) + "\n").encode("utf-8")


def atomic_write(path, data):
    descriptor, temp_name = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    try:
        with os.fdopen(descriptor, "wb") as handle:
            handle.write(data)
            handle.flush()
            os.fsync(handle.fileno())
        os.chmod(temp_name, 0o644)
        os.replace(temp_name, path)
    except BaseException:
        try:
            os.unlink(temp_name)
        except FileNotFoundError:
            pass
        raise


HEADERS = {
    "tag": "# Reviewed release tag signers (OpenSSH allowed-signers; namespace git).\n"
           "# Add keys only with: scripts/enroll-release-signer.py --purpose tag --principal PRINCIPAL --public-key-file FILE",
    "evidence": "# Reviewed evidence signers (OpenSSH allowed-signers; Telos beta namespaces).\n"
                "# Add keys only with: scripts/enroll-release-signer.py --purpose evidence --principal PRINCIPAL --public-key-file FILE",
}


def enroll(arguments):
    file_name, namespaces = PURPOSES[arguments.purpose]
    path = arguments.release_dir / file_name
    if not PRINCIPAL.match(arguments.principal):
        fail("principal may contain only letters, digits and . _ @ + -")
    key_path = Path(arguments.public_key_file)
    if key_path.stat().st_size > 4096:
        fail("public key file is unreasonably large")
    key_type, blob = parse_public_key(key_path.read_text(encoding="utf-8"))
    fingerprint = fingerprint_of(key_type, blob)
    print(f"fingerprint: {fingerprint}")
    confirmation = arguments.confirm_fingerprint
    if confirmation is None:
        if not sys.stdin.isatty():
            fail("explicit fingerprint confirmation is required (--confirm-fingerprint)")
        confirmation = input("Type the fingerprint above to enroll this signer: ").strip()
    if not FINGERPRINT.match(confirmation) or confirmation != fingerprint:
        fail("fingerprint confirmation did not match; nothing was written")
    entries = read_entries(path)
    entries.append((arguments.principal, namespaces, key_type, blob))
    check_unique(entries, file_name)
    atomic_write(path, render(entries, HEADERS[arguments.purpose]))
    print(f"enrolled {arguments.principal} in {file_name}")


def verify_files(arguments):
    enrolled = 0
    for purpose, (file_name, _) in PURPOSES.items():
        path = arguments.release_dir / file_name
        if not path.exists():
            fail(f"{file_name} is missing")
        entries = read_entries(path)
        check_unique(entries, file_name)
        for principal, namespaces, key_type, blob in entries:
            print(f"{file_name}: {principal} {fingerprint_of(key_type, blob)} namespaces={','.join(namespaces)}")
        enrolled += len(entries)
        if not entries:
            print(f"{file_name}: no reviewed signer is enrolled")
    legacy = arguments.release_dir / "telos-release.pub"
    if legacy.exists():
        fail("release/telos-release.pub is invalid trust data and must be removed")
    return enrolled


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--release-dir", type=Path, default=DEFAULT_RELEASE_DIR)
    parser.add_argument("--verify-files", action="store_true")
    parser.add_argument("--purpose", choices=sorted(PURPOSES))
    parser.add_argument("--principal")
    parser.add_argument("--public-key-file")
    parser.add_argument("--confirm-fingerprint")
    arguments = parser.parse_args(argv)
    try:
        if arguments.verify_files:
            if any([arguments.purpose, arguments.principal, arguments.public_key_file]):
                fail("--verify-files takes no enrollment arguments")
            enrolled = verify_files(arguments)
            if enrolled == 0 or any(not read_entries(arguments.release_dir / name) for name, _ in PURPOSES.values()):
                print("not_run: a reviewed tag signer and evidence signer must both be enrolled", file=sys.stderr)
                return EXIT_NOT_RUN
            return 0
        if not (arguments.purpose and arguments.principal and arguments.public_key_file):
            parser.error("--purpose, --principal and --public-key-file are required to enroll")
        enroll(arguments)
        return 0
    except (EnrollError, OSError, UnicodeDecodeError) as error:
        print(f"enroll-release-signer: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
