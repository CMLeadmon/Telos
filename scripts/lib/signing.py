#!/usr/bin/env python3
"""OpenSSH signature wrappers with fixed, purpose-separated namespaces.

Evidence, certification, review and guided records each use their own
namespace, so a signature made for one purpose can never be replayed as another.
The wrappers return the verified signer fingerprint and raise SigningError for
every failure; they never print key material.
"""

import os
import re
import subprocess
import tempfile
from pathlib import Path

NAMESPACES = {
    "evidence": "telos-beta-evidence-v1",
    "certification": "telos-beta-certification-v1",
    "review": "telos-beta-review-v1",
    "guided": "telos-guided-smoke-v1",
}
GOOD_SIGNATURE = re.compile(
    r'Good "(?P<namespace>[^"]+)" signature for (?P<principal>\S+) with \S+ key (?P<fingerprint>SHA256:[A-Za-z0-9+/]{43})'
)
PRINCIPAL = re.compile(r"[A-Za-z0-9._@+-]{1,128}\Z")


class SigningError(Exception):
    pass


def _run(arguments, data=None):
    try:
        return subprocess.run(
            arguments, input=data, capture_output=True, check=False, timeout=60
        )
    except FileNotFoundError as error:
        raise SigningError("ssh-keygen is not installed") from error
    except subprocess.TimeoutExpired as error:
        raise SigningError("ssh-keygen timed out") from error


def sign_file(private_key, namespace, data_path):
    """Sign ``data_path`` and return the signature bytes. The key must be mode 0600."""
    if namespace not in NAMESPACES.values():
        raise SigningError(f"unknown namespace {namespace!r}")
    key = Path(private_key)
    mode = key.stat().st_mode & 0o777
    if mode & 0o077:
        raise SigningError("signing key must not be accessible by group or others")
    with tempfile.TemporaryDirectory() as scratch:
        copy = Path(scratch) / "payload"
        copy.write_bytes(Path(data_path).read_bytes())
        done = _run(["ssh-keygen", "-q", "-Y", "sign", "-f", str(key), "-n", namespace, str(copy)])
        if done.returncode != 0:
            raise SigningError("signing failed")
        return Path(str(copy) + ".sig").read_bytes()


def verify_signature(allowed_signers, principal, namespace, data, signature):
    """Return the verified signer fingerprint, or raise SigningError."""
    if namespace not in NAMESPACES.values():
        raise SigningError(f"unknown namespace {namespace!r}")
    if not PRINCIPAL.match(principal):
        raise SigningError("invalid principal")
    with tempfile.TemporaryDirectory() as scratch:
        signature_file = Path(scratch) / "payload.sig"
        signature_file.write_bytes(signature)
        done = _run(
            ["ssh-keygen", "-Y", "verify", "-f", str(allowed_signers), "-I", principal,
             "-n", namespace, "-s", str(signature_file)],
            data,
        )
    if done.returncode != 0:
        raise SigningError("signature verification failed")
    match = GOOD_SIGNATURE.search(done.stdout.decode("utf-8", "replace"))
    if match is None or match.group("namespace") != namespace or match.group("principal") != principal:
        raise SigningError("signature verification produced no matching result")
    return match.group("fingerprint")
