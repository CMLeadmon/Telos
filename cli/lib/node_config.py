#!/usr/bin/env python3
"""Parse, validate, write and initialize a Telos node's persistent configuration.

The Bash CLI delegates every byte-exact or security-sensitive operation here so no
shell ever evaluates configuration: ``node.env`` is data, never sourced. Secret
values enter only through an inherited file descriptor, never argv or the process
environment, and diagnostics never print a secret.

Encoding (Compose-compatible, python-dotenv semantics): a value is either unquoted
from the safe set ``[A-Za-z0-9_@%+=:,./-]`` or single-quoted where ``\\\\`` is a
backslash and ``\\'`` an apostrophe. Nothing else is accepted, so ``$``, backticks,
substitutions, multiline values and NUL cannot reach Compose through this file.
"""

import argparse
import base64
import binascii
import errno
import fcntl
import json
import os
import re
import secrets as secrets_module
import shutil
import signal
import stat
import subprocess
import sys
import tempfile

KEY_PATTERN = re.compile(r"[A-Z][A-Z0-9_]*\Z")
SAFE_UNQUOTED = re.compile(r"[A-Za-z0-9_@%+=:,./-]*\Z")
PLACEHOLDER = re.compile(r"change-me|generate-me|^$", re.I)
EXAMPLE_DOMAIN = re.compile(r"(^|\.)example\.(com|org|net)$|^localhost$|\.invalid$", re.I)
RUNTIME_UID = 10001
RUNTIME_GID = 10001
MIN_KEY_BYTES = 32
CURSOR_CONTAINER_PATH = "/run/telos/cursor-keys.json"
HLS_CONTAINER_PATH = "/run/telos/hls-key"
INIT_SECRETS = (
    "POSTGRES_PASSWORD", "TELOS_OWNER_PASSWORD", "TELOS_RUNTIME_PASSWORD", "REDIS_PASSWORD",
    "GRIMMORY_ADMIN_PASSWORD", "GRIMMORY_DB_PASSWORD", "MARIADB_ROOT_PASSWORD", "TELOS_BOOTSTRAP_TOKEN",
)
PRODUCTION_SECRETS = INIT_SECRETS + ("JELLYFIN_ADMIN_TOKEN",)


class ConfigError(Exception):
    """A configuration or invocation problem with a safe, printable message."""


def fail(message):
    raise ConfigError(message)


# --------------------------------------------------------------------------- parsing

def decode_value(raw, where):
    value = _decode_value(raw, where)
    if "${" in value:
        fail(f"{where}: value contains ${{ which Compose would expand; use a different secret")
    return value


def _decode_value(raw, where):
    if raw.startswith("'"):
        if len(raw) < 2 or not raw.endswith("'"):
            fail(f"{where}: unterminated single-quoted value")
        body = raw[1:-1]
        out, index = [], 0
        while index < len(body):
            char = body[index]
            if char == "\\":
                if index + 1 >= len(body) or body[index + 1] not in "\\'":
                    fail(f"{where}: unsupported escape in quoted value")
                out.append(body[index + 1])
                index += 2
            elif char == "'":
                fail(f"{where}: unescaped apostrophe in quoted value")
            else:
                out.append(char)
                index += 1
        return "".join(out)
    if not SAFE_UNQUOTED.match(raw):
        fail(f"{where}: unquoted value contains characters that need single quotes")
    return raw


def encode_value(value):
    if "\0" in value or "\n" in value or "\r" in value:
        fail("a value may not contain NUL or a line break")
    if "${" in value:
        # python-dotenv (podman-compose) interpolates ${NAME} inside every value,
        # quoted or not, so the sequence cannot be preserved through Compose.
        fail("a value may not contain the sequence ${ (Compose would expand it)")
    if value and SAFE_UNQUOTED.match(value):
        return value
    return "'" + value.replace("\\", "\\\\").replace("'", "\\'") + "'"


def parse_env_text(text, name="node.env"):
    """Return (values, lines) for strict node.env text; raise ConfigError on any defect."""
    if "\0" in text:
        fail(f"{name}: contains a NUL byte")
    values, lines = {}, text.splitlines(keepends=True)
    for number, line in enumerate(lines, 1):
        stripped = line.rstrip("\n").rstrip("\r")
        if not stripped.strip() or stripped.lstrip().startswith("#"):
            continue
        if "=" not in stripped:
            fail(f"{name}:{number}: not an assignment")
        key, raw = stripped.split("=", 1)
        if not KEY_PATTERN.match(key):
            fail(f"{name}:{number}: invalid variable name {key[:40]!r}")
        if key in values:
            fail(f"{name}:{number}: duplicate variable {key}")
        values[key] = decode_value(raw, f"{name}:{number}")
    return values, lines


def read_text(path):
    try:
        info = os.lstat(path)
    except FileNotFoundError:
        fail(f"{path} does not exist")
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISREG(info.st_mode):
        fail(f"{path} must be a regular file, not a link")
    with open(path, "r", encoding="utf-8") as handle:
        return handle.read()


def load_env(path):
    return parse_env_text(read_text(path), os.path.basename(path))[0]


# --------------------------------------------------------------------------- atomic IO

def fsync_directory(path):
    descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_DIRECTORY", 0))
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def atomic_replace(path, data, mode=0o600):
    """Replace an existing file via a sibling temp: all-or-nothing, mode preserved."""
    directory = os.path.dirname(path)
    descriptor, temp = tempfile.mkstemp(prefix=f".{os.path.basename(path)}.", dir=directory)
    try:
        with os.fdopen(descriptor, "wb") as handle:
            handle.write(data)
            handle.flush()
            os.fsync(handle.fileno())
        os.chmod(temp, mode)
        os.replace(temp, path)
    except BaseException:
        try:
            os.unlink(temp)
        except FileNotFoundError:
            pass
        raise
    fsync_directory(directory)


def exclusive_create(path, data, mode=0o600):
    """Create ``path`` without ever replacing an existing file."""
    directory = os.path.dirname(path)
    descriptor, temp = tempfile.mkstemp(prefix=f".{os.path.basename(path)}.", dir=directory)
    try:
        with os.fdopen(descriptor, "wb") as handle:
            handle.write(data)
            handle.flush()
            os.fsync(handle.fileno())
        os.chmod(temp, mode)
        try:
            os.link(temp, path)
        except FileExistsError:
            fail(f"refusing to overwrite existing {path}")
    finally:
        try:
            os.unlink(temp)
        except FileNotFoundError:
            pass
    fsync_directory(directory)


def set_value(path, key, value):
    """Rewrite one assignment, leaving every other byte of the file untouched."""
    if not KEY_PATTERN.match(key):
        fail(f"invalid variable name {key!r}")
    text = read_text(path)
    values, lines = parse_env_text(text, os.path.basename(path))
    encoded = f"{key}={encode_value(value)}\n"
    if key in values:
        for index, line in enumerate(lines):
            if line.split("=", 1)[0] == key and not line.lstrip().startswith("#"):
                ending = "\r\n" if line.endswith("\r\n") else "\n"
                lines[index] = encoded.rstrip("\n") + ending
                break
    else:
        if lines and not lines[-1].endswith("\n"):
            lines[-1] += "\n"
        lines.append(encoded)
    new_text = "".join(lines)
    parse_env_text(new_text, os.path.basename(path))
    mode = stat.S_IMODE(os.stat(path).st_mode)
    atomic_replace(path, new_text.encode("utf-8"), mode)


# --------------------------------------------------------------------------- node path

def resolve_node_path(requested, development, repo_root):
    if requested:
        path = requested
    elif development:
        path = os.path.join(repo_root, ".telos")
    else:
        path = "/var/lib/telos"
    if not os.path.isabs(path) or os.path.normpath(path) != path or path == "/":
        fail(f"TELOS_NODE_PATH must be an absolute, normalized path (got {path!r})")
    current = "/"
    for part in path.strip("/").split("/"):
        current = os.path.join(current, part)
        try:
            info = os.lstat(current)
        except FileNotFoundError:
            break
        if stat.S_ISLNK(info.st_mode):
            fail(f"{current} is a symbolic link; the node path may not be redirected")
        if not stat.S_ISDIR(info.st_mode):
            fail(f"{current} is not a directory")
    return path


def check_node_directory_trust(path, production):
    """Production nodes must live under directories only root can rearrange."""
    if not production:
        return
    current = "/"
    for part in [""] + path.strip("/").split("/"):
        current = os.path.join(current, part) if part else "/"
        try:
            info = os.lstat(current)
        except FileNotFoundError:
            continue
        if info.st_uid != 0:
            fail(f"{current} must be owned by root for a production node")
        if info.st_mode & (stat.S_IWGRP | stat.S_IWOTH) and not info.st_mode & stat.S_ISVTX:
            fail(f"{current} must not be writable by group or others")


# --------------------------------------------------------------------------- signing files

def decode_key(text, label, minimum=MIN_KEY_BYTES):
    try:
        raw = base64.b64decode(text.strip(), validate=True)
    except (binascii.Error, ValueError):
        fail(f"{label} is not valid base64")
    if len(raw) < minimum:
        fail(f"{label} must decode to at least {minimum} bytes (got {len(raw)})")
    return raw


def validate_signing(directory, runtime_uid, runtime_gid, strict_owner):
    """Return a list of defects (empty when the signing directory is sound)."""
    problems = []
    try:
        info = os.lstat(directory)
    except FileNotFoundError:
        return [f"signing directory {directory} does not exist (restore it from backup; keys are never regenerated on an existing node)"]
    if stat.S_ISLNK(info.st_mode):
        return [f"signing directory {directory} is a symbolic link"]
    if not stat.S_ISDIR(info.st_mode):
        return [f"signing path {directory} is not a directory"]
    if info.st_mode & 0o077:
        problems.append(f"signing directory is mode {stat.S_IMODE(info.st_mode):o}; require 0700 or stricter (chmod 700 {directory})")
    if (info.st_mode & 0o500) != 0o500:
        problems.append("signing directory must grant its owner read and search")
    if strict_owner and (info.st_uid, info.st_gid) != (runtime_uid, runtime_gid):
        problems.append(
            f"signing directory is owned {info.st_uid}:{info.st_gid}; the gateway runs as {runtime_uid}:{runtime_gid} "
            f"(chown {runtime_uid}:{runtime_gid} {directory})")
    material = {}
    for name in ("cursor-keys.json", "hls-key"):
        path = os.path.join(directory, name)
        try:
            file_info = os.lstat(path)
        except FileNotFoundError:
            problems.append(f"{name} is missing (restore it from backup; never regenerate on an existing node)")
            continue
        except PermissionError:
            problems.append(f"{name} cannot be inspected: the signing directory is not searchable by this user (chmod 700 {directory})")
            continue
        if stat.S_ISLNK(file_info.st_mode) or not stat.S_ISREG(file_info.st_mode):
            problems.append(f"{name} must be a regular file, not a link")
            continue
        if file_info.st_mode & 0o177:
            problems.append(f"{name} is mode {stat.S_IMODE(file_info.st_mode):o}; require 0600 or stricter (chmod 600 {path})")
        if not file_info.st_mode & 0o400:
            problems.append(f"{name} is not readable by its owner")
        if strict_owner and (file_info.st_uid, file_info.st_gid) != (runtime_uid, runtime_gid):
            problems.append(f"{name} is owned {file_info.st_uid}:{file_info.st_gid}; expected {runtime_uid}:{runtime_gid}")
        try:
            material[name] = open(path, "r", encoding="utf-8").read()
        except OSError as error:
            problems.append(f"{name} cannot be read: {error.strerror}")
    try:
        if "cursor-keys.json" in material:
            keyring = json.loads(material["cursor-keys.json"])
            if not isinstance(keyring, dict) or set(keyring) != {"active", "keys"} or not isinstance(keyring["keys"], dict) \
                    or not keyring["keys"] or not isinstance(keyring["active"], str):
                fail("cursor-keys.json must be {\"active\": id, \"keys\": {id: base64}}")
            if keyring["active"] not in keyring["keys"]:
                fail("cursor-keys.json active key is not in the ring")
            decoded = [decode_key(value, f"cursor key {identifier}") for identifier, value in keyring["keys"].items()]
            if len(set(decoded)) != len(decoded):
                fail("cursor keys must be distinct")
            if "hls-key" in material:
                hls = decode_key(material["hls-key"], "hls-key")
                if hls in decoded:
                    fail("the HLS signing key must differ from every cursor key")
        elif "hls-key" in material:
            decode_key(material["hls-key"], "hls-key")
    except (ConfigError, json.JSONDecodeError) as error:
        problems.append(str(error) if isinstance(error, ConfigError) else "cursor-keys.json is not valid JSON")
    return problems


# --------------------------------------------------------------------------- node validation

def library_ids(value):
    return [item for item in (part.strip() for part in value.split(",")) if item]


def validate_node(path, mode, repo_root, runtime_uid=None, runtime_gid=None, strict_owner=None, only="all"):
    """Return a list of defects for a node.env in ``production`` or ``development`` mode.

    ``only`` limits the report to ``signing`` or ``integrations`` (syntax and mode
    problems are always reported) so doctor can name one repair per defect.
    """
    problems = []
    try:
        values = load_env(path)
    except ConfigError as error:
        return [str(error)]
    production = mode == "production"
    if only == "all":
        if production and values.get("TELOS_ENV") != "production":
            problems.append("TELOS_ENV must be production")
        if not production and values.get("TELOS_ENV") not in (None, "development"):
            problems.append("TELOS_ENV must be development in a development node")
    if only in ("all", "signing"):
        config_path = values.get("TELOS_CONFIG_PATH", "")
        if not config_path or not os.path.isabs(config_path):
            problems.append("TELOS_CONFIG_PATH must be an absolute host path")
        if values.get("TELOS_CURSOR_KEYS_FILE") != CURSOR_CONTAINER_PATH:
            problems.append(f"TELOS_CURSOR_KEYS_FILE must be {CURSOR_CONTAINER_PATH}")
        if values.get("TELOS_HLS_SIGNING_KEY_FILE") != HLS_CONTAINER_PATH:
            problems.append(f"TELOS_HLS_SIGNING_KEY_FILE must be {HLS_CONTAINER_PATH}")
        if config_path and os.path.isabs(config_path):
            uid = RUNTIME_UID if runtime_uid is None else runtime_uid
            gid = RUNTIME_GID if runtime_gid is None else runtime_gid
            owner_check = production if strict_owner is None else strict_owner
            problems += validate_signing(config_path, uid, gid, owner_check)
    if production and only in ("all", "integrations"):
        for key in PRODUCTION_SECRETS:
            if PLACEHOLDER.search(values.get(key, "")):
                problems.append(f"{key} is a placeholder; run `telos configure integrations`" if key == "JELLYFIN_ADMIN_TOKEN"
                                else f"{key} is missing or a placeholder")
        for key in ("GRIMMORY_ADMIN_USER",):
            if PLACEHOLDER.search(values.get(key, "")):
                problems.append(f"{key} is not set; run `telos configure integrations`")
        if not library_ids(values.get("JELLYFIN_LIBRARY_IDS", "")):
            problems.append("JELLYFIN_LIBRARY_IDS names no library; run `telos configure integrations`")
        if not library_ids(values.get("GRIMMORY_LIBRARY_IDS", "")):
            problems.append("GRIMMORY_LIBRARY_IDS names no library; run `telos configure integrations`")
        domain = values.get("TELOS_DOMAIN", "")
        if not domain or EXAMPLE_DOMAIN.search(domain):
            problems.append("TELOS_DOMAIN is unset or an example/loopback domain")
        email = values.get("ACME_EMAIL", "")
        if not email or EXAMPLE_DOMAIN.search(email.rsplit("@", 1)[-1]):
            problems.append("ACME_EMAIL is unset or an example address")
        if not values.get("STORAGE_PATH", "").startswith("/"):
            problems.append("STORAGE_PATH must be an absolute path")
    return problems


# --------------------------------------------------------------------------- init

def run_openssl(*arguments):
    try:
        done = subprocess.run(["openssl", "rand", *arguments], capture_output=True, text=True, check=True, timeout=30)
    except FileNotFoundError:
        fail("openssl is required to generate secrets")
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired):
        fail("openssl failed to generate a secret")
    value = done.stdout.strip()
    if not value:
        fail("openssl produced no output")
    return value


class Initializer:
    """One node initialization. Owns the staging paths it creates and nothing else."""

    def __init__(self, arguments):
        self.arguments = arguments
        self.created = []  # paths this invocation made and may remove on failure
        self.lock_descriptor = None
        self.interrupted = None

    def interrupt(self, signum, _frame):
        self.interrupted = signum
        raise KeyboardInterrupt

    def cleanup(self):
        for path in reversed(self.created):
            try:
                if os.path.isdir(path) and not os.path.islink(path):
                    shutil.rmtree(path)
                elif os.path.lexists(path):
                    os.unlink(path)
            except OSError:
                pass
        self.created = []

    def acquire_lock(self, node):
        path = os.path.join(node, ".init.lock")
        descriptor = os.open(path, os.O_RDWR | os.O_CREAT | getattr(os, "O_NOFOLLOW", 0), 0o600)
        try:
            fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except OSError as error:
            os.close(descriptor)
            if error.errno in (errno.EAGAIN, errno.EACCES):
                fail("another telos init is running for this node; wait for it to finish")
            raise
        self.lock_descriptor = descriptor

    def run(self):
        a = self.arguments
        production = a.mode == "production"
        node = resolve_node_path(a.node_path, not production, a.repo_root)
        if production and os.geteuid() != 0:
            fail("production initialization must run as root (sudo telos init)")
        check_node_directory_trust(os.path.dirname(node), production)
        if not os.path.exists(node):
            parent = os.path.dirname(node)
            if not os.path.isdir(parent):
                fail(f"parent directory {parent} does not exist")
            os.mkdir(node, 0o755 if production else 0o700)
            self.created_node = node
        info = os.lstat(node)
        if production and (info.st_uid != 0 or info.st_mode & 0o022):
            fail(f"{node} must be owned by root and not group/other writable")
        self.acquire_lock(node)

        env_path = os.path.join(node, "node.env")
        config = os.path.join(node, "config")
        if os.path.lexists(env_path):
            fail(f"{env_path} already exists; this node is initialized. Run `telos doctor` for diagnosis.")
        if os.path.lexists(config):
            fail(f"orphan configuration directory {config} exists without node.env. Inspect it, move it aside "
                 f"(it may hold the only copy of this node's signing keys), then re-run init. init never deletes it.")
        stale = sorted(name for name in os.listdir(node) if name.startswith(".config-staging."))
        if stale:
            fail(f"a previous init left {os.path.join(node, stale[0])}; inspect and remove it yourself, then re-run")
        imported = parse_legacy_env(a.import_env) if a.import_env else None
        if a.legacy_env and not a.import_env and os.path.lexists(a.legacy_env):
            fail(f"a checkout .env exists at {a.legacy_env}. Do not regenerate secrets: import it with "
                 f"`telos init --import-env {a.legacy_env}`, which validates and copies the existing values.")

        template = read_text(a.template)
        marker = secrets_module.token_hex(8)
        staging = os.path.join(node, f".config-staging.{marker}")

        previous = {sig: signal.signal(sig, self.interrupt) for sig in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP)}
        try:
            os.mkdir(staging, 0o700)
            self.created.append(staging)
            with open(os.path.join(staging, ".telos-init-marker"), "w", encoding="utf-8") as handle:
                handle.write(marker + "\n")
            keyring_a = run_openssl("-base64", "32")
            keyring = json.dumps({"active": "k1", "keys": {"k1": keyring_a}}, separators=(",", ":")) + "\n"
            hls = run_openssl("-base64", "32")
            for name, content in (("cursor-keys.json", keyring), ("hls-key", hls + "\n")):
                exclusive_create(os.path.join(staging, name), content.encode("ascii"), 0o600)
            os.unlink(os.path.join(staging, ".telos-init-marker"))
            if production:
                for path in (staging, os.path.join(staging, "cursor-keys.json"), os.path.join(staging, "hls-key")):
                    os.chown(path, RUNTIME_UID, RUNTIME_GID)
            fsync_directory(staging)
            os.rename(staging, config)
            self.created.remove(staging)
            self.created.append(config)
            fsync_directory(node)

            if imported is None:
                replacements = {key: run_openssl("-hex", "24") for key in INIT_SECRETS}
                values = dict(a.assignments)
            else:
                # Importing an existing checkout .env keeps every credential the
                # database and upstream volumes already hold; only the signing
                # keys above are new. The source file is never moved or deleted.
                replacements = {}
                values = dict(imported)
                values.update(a.assignments)
            values.update({
                "TELOS_NODE_PATH": node,
                "TELOS_CONFIG_PATH": config,
                "TELOS_CURSOR_KEYS_FILE": CURSOR_CONTAINER_PATH,
                "TELOS_HLS_SIGNING_KEY_FILE": HLS_CONTAINER_PATH,
                "TELOS_ENV": a.mode,
            })
            values.update(replacements)
            text = template
            for key, value in values.items():
                text = apply_assignment(text, key, value)
            parse_env_text(text, "node.env")
            exclusive_create(env_path, text.encode("utf-8"), 0o600)
            self.created.remove(config)  # committed: past this point nothing is removed
            for sub in ("state",):
                directory = os.path.join(node, sub)
                if not os.path.isdir(directory):
                    os.mkdir(directory, 0o700)
            fsync_directory(node)
        except KeyboardInterrupt:
            self.cleanup()
            fail(f"interrupted by signal {self.interrupted}; staging removed, no node.env written")
        except BaseException:
            self.cleanup()
            raise
        finally:
            for sig, handler in previous.items():
                signal.signal(sig, handler)
            if self.lock_descriptor is not None:
                os.close(self.lock_descriptor)
        return node, env_path


def parse_legacy_env(path):
    """Read a pre-node.env checkout .env tolerantly and return validated values."""
    text = read_text(path)
    values = {}
    for number, raw in enumerate(text.splitlines(), 1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        if "=" not in line:
            fail(f"{os.path.basename(path)}:{number}: not an assignment")
        key, value = line.split("=", 1)
        key = key.strip()
        if not KEY_PATTERN.match(key):
            fail(f"{os.path.basename(path)}:{number}: invalid variable name {key[:40]!r}")
        if key in values:
            fail(f"{os.path.basename(path)}:{number}: duplicate variable {key}")
        value = value.strip()
        if len(value) >= 2 and value[0] == value[-1] and value[0] in "'\"":
            value = value[1:-1]
        if "\0" in value or "\n" in value or "\r" in value:
            fail(f"{os.path.basename(path)}:{number}: unsupported characters in {key}")
        values[key] = value
    for reserved in ("TELOS_NODE_PATH", "TELOS_CONFIG_PATH", "TELOS_CURSOR_KEYS_FILE", "TELOS_HLS_SIGNING_KEY_FILE"):
        values.pop(reserved, None)
    return values


def apply_assignment(text, key, value):
    lines = text.splitlines(keepends=True)
    encoded = f"{key}={encode_value(value)}\n"
    for index, line in enumerate(lines):
        if line.startswith(key + "=") and not line.lstrip().startswith("#"):
            lines[index] = encoded
            return "".join(lines)
    if lines and not lines[-1].endswith("\n"):
        lines[-1] += "\n"
    return "".join(lines) + encoded


# --------------------------------------------------------------------------- CLI

def read_fd_value(descriptor):
    with os.fdopen(os.dup(descriptor), "rb") as handle:
        data = handle.read()
    if data.endswith(b"\n"):
        data = data[:-1]
    return data.decode("utf-8")


def parse_assignments(items):
    assignments = {}
    for item in items or []:
        key, _, value = item.partition("=")
        if not KEY_PATTERN.match(key):
            fail(f"invalid assignment {key!r}")
        assignments[key] = value
    return assignments


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("parse").add_argument("file")
    get = commands.add_parser("get")
    get.add_argument("file")
    get.add_argument("key")
    setter = commands.add_parser("set")
    setter.add_argument("file")
    setter.add_argument("key")
    setter.add_argument("--value-fd", type=int, required=True)
    validate = commands.add_parser("validate")
    validate.add_argument("file")
    validate.add_argument("--mode", choices=("production", "development"), required=True)
    validate.add_argument("--repo-root", default=".")
    validate.add_argument("--runtime-uid", type=int)
    validate.add_argument("--runtime-gid", type=int)
    validate.add_argument("--no-owner-check", action="store_true")
    validate.add_argument("--only", choices=("all", "signing", "integrations"), default="all")
    resolve = commands.add_parser("resolve")
    resolve.add_argument("--repo-root", required=True)
    resolve.add_argument("--dev", action="store_true")
    initialize = commands.add_parser("init")
    initialize.add_argument("--mode", choices=("production", "development"), required=True)
    initialize.add_argument("--node-path")
    initialize.add_argument("--repo-root", required=True)
    initialize.add_argument("--template", required=True)
    initialize.add_argument("--legacy-env")
    initialize.add_argument("--import-env")
    initialize.add_argument("--set", dest="set_items", action="append", default=[])
    arguments = parser.parse_args(argv)
    try:
        if arguments.command == "parse":
            load_env(arguments.file)
        elif arguments.command == "get":
            values = load_env(arguments.file)
            if arguments.key not in values:
                return 1
            sys.stdout.write(values[arguments.key] + "\n")
        elif arguments.command == "set":
            set_value(arguments.file, arguments.key, read_fd_value(arguments.value_fd))
        elif arguments.command == "validate":
            problems = validate_node(
                arguments.file, arguments.mode, arguments.repo_root,
                arguments.runtime_uid, arguments.runtime_gid,
                False if arguments.no_owner_check else None, arguments.only)
            for problem in problems:
                print(problem, file=sys.stderr)
            return 1 if problems else 0
        elif arguments.command == "resolve":
            path = resolve_node_path(os.environ.get("TELOS_NODE_PATH", ""), arguments.dev, arguments.repo_root)
            print(path)
        elif arguments.command == "init":
            arguments.assignments = parse_assignments(arguments.set_items)
            arguments.node_path = arguments.node_path or os.environ.get("TELOS_NODE_PATH", "")
            node, env_path = Initializer(arguments).run()
            print(env_path)
    except ConfigError as error:
        print(f"node-config: {error}", file=sys.stderr)
        return 1
    except OSError as error:
        print(f"node-config: {error.strerror or error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
