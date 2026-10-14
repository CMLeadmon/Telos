#!/usr/bin/env python3
"""Turn the source Compose model into the no-build model a release generation runs.

    release_compose.py derive --root SRC --out-dir DIR      write DIR/docker-compose.yml and DIR/docker-compose.setup.yml
    release_compose.py verify --dir DIR [--lock FILE]       prove a directory's Compose files are a complete no-build model

podman-compose 1.6.0 cannot remove a `build:` key with an override (`!reset` crashes it), so an overlay
alone cannot make the source model run without building. The generation therefore carries a *derived*
base model: every `build:` block is removed (comments elsewhere survive; the transformation is
line-based and deterministic) and every third-party `image:` is rewritten to the exact linux/amd64
platform manifest recorded in release/images.lock -- the digest of the archive the builder ships, so it
resolves from local storage with no registry. The services Telos builds itself get their image from
docker-compose.release.yml, which requires the candidate's image variables (images.env) and fails closed
without them.
"""

import argparse
import json
import os
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
BASE_FILES = ("docker-compose.yml", "docker-compose.setup.yml")
OVERLAY = "docker-compose.release.yml"
DIGEST = re.compile(r"sha256:[0-9a-f]{64}\Z")
# service -> the image variable the release overlay must require for it
BUILT_SERVICES = {
    "telos-core": "TELOS_CORE_IMAGE",
    "telos-migrate": "TELOS_CORE_IMAGE",
    "telos-sanitize-restore": "TELOS_CORE_IMAGE",
    "telos-audiobook-migrate": "TELOS_CORE_IMAGE",
    "telos-setup-proxy": "TELOS_CORE_IMAGE",
    "telos-client": "TELOS_CLIENT_IMAGE",
    "telos-egress-proxy": "TELOS_EGRESS_PROXY_IMAGE",
}
SERVICE_LINE = re.compile(r"^  ([a-z][a-z0-9-]*):\s*(?:#.*)?$")
KEY_LINE = re.compile(r"^(\s*)([A-Za-z_<][^:]*):(.*)$")
IMAGE_LINE = re.compile(r"^(    image: )(\S+)\s*$")


class ComposeError(Exception):
    pass


def fail(message):
    raise ComposeError(message)


def load_lock(path=None):
    path = Path(path) if path else ROOT / "release" / "images.lock"
    return json.loads(path.read_text(encoding="utf-8"))


def lock_index(lock):
    """source ('docker.io/library/redis:7-alpine') -> (repository, platform digest, name)."""
    index = {}
    for name, entry in lock["images"].items():
        platform = entry["platforms"].get("linux/amd64")
        if not platform or not DIGEST.match(platform):
            fail(f"images.lock has no linux/amd64 digest for {name}")
        source = entry["source"]
        repository = source.rsplit(":", 1)[0] if ":" in source.rsplit("/", 1)[-1] else source
        index[source] = (repository, platform, name)
    return index


def third_party_reference(image, index):
    """`docker.io/library/redis:7-alpine@sha256:<index>` -> `docker.io/library/redis@sha256:<platform>`."""
    source = image.split("@", 1)[0]
    if source not in index:
        fail(f"{image} is not in release/images.lock; a release image must be locked")
    repository, platform, _ = index[source]
    return f"{repository}@{platform}"


def derive_text(text, index, name):
    """Remove `build:` blocks and lock every remaining image. Returns (new text, built service names)."""
    output, built = [], []
    service, skipping = None, False
    for line in text.splitlines():
        if skipping:
            stripped = line.strip()
            if not stripped or len(line) - len(line.lstrip()) > 4:
                continue   # inside (or trailing blank space of) the build block
            skipping = False
        match = SERVICE_LINE.match(line)
        if match:
            service = match.group(1)
        key = KEY_LINE.match(line)
        if service and key and key.group(1) == "    " and key.group(2) == "build":
            built.append(service)
            skipping = True
            continue
        image = IMAGE_LINE.match(line)
        if service and image:
            line = image.group(1) + third_party_reference(image.group(2), index)
        output.append(line)
    result = "\n".join(output)
    if text.endswith("\n"):
        result += "\n"
    if "\n    build:" in result or "\nx-build" in result:
        fail(f"{name}: a build key survived the transformation")
    return result, built


def derive(root, out_dir, lock=None):
    root, out_dir = Path(root), Path(out_dir)
    index = lock_index(lock or load_lock(root / "release" / "images.lock"))
    out_dir.mkdir(parents=True, exist_ok=True)
    all_built = set()
    for name in BASE_FILES:
        source = root / name
        text, built = derive_text(source.read_text(encoding="utf-8"), index, name)
        all_built |= set(built)
        (out_dir / name).write_text(text, encoding="utf-8")
        os.chmod(out_dir / name, 0o644)
    if all_built != set(BUILT_SERVICES):
        fail(f"the source model builds {sorted(all_built)}, but the release overlay covers {sorted(BUILT_SERVICES)}")
    overlay = root / OVERLAY
    if not overlay.is_file():
        fail(f"{OVERLAY} is missing")
    (out_dir / OVERLAY).write_bytes(overlay.read_bytes())
    os.chmod(out_dir / OVERLAY, 0o644)
    return sorted(all_built)


# ------------------------------------------------------------------ verification

def _yaml(path):
    import yaml
    return yaml.safe_load(Path(path).read_text(encoding="utf-8")) or {}


def merged_services(directory):
    """The services of base + setup + release overlay, merged the way Compose merges mappings."""
    merged = {}
    for name in (*BASE_FILES, OVERLAY):
        for service, body in (_yaml(Path(directory) / name).get("services") or {}).items():
            merged.setdefault(service, {}).update(body or {})
    return merged


def verify(directory, lock=None):
    """A directory's Compose files are a complete no-build model whose images are all digest-locked."""
    directory = Path(directory)
    problems = []
    for name in (*BASE_FILES, OVERLAY):
        if not (directory / name).is_file():
            problems.append(f"{name} is missing")
    if problems:
        return problems
    index = lock_index(lock or load_lock(ROOT / "release" / "images.lock"))
    locked = {f"{repository}@{platform}" for repository, platform, _ in index.values()}
    services = merged_services(directory)
    for name in (*BASE_FILES, OVERLAY):
        raw = (directory / name).read_text(encoding="utf-8")
        if re.search(r"^\s+build:", raw, re.M):
            problems.append(f"{name} still contains a build key")
    for service, body in sorted(services.items()):
        if "build" in body:
            problems.append(f"{service}: a build key survived")
        image = body.get("image")
        if not isinstance(image, str):
            problems.append(f"{service}: no image")
            continue
        variable = BUILT_SERVICES.get(service)
        if variable:
            if not re.fullmatch(r"\$\{" + variable + r":\?[^}]+\}", image):
                problems.append(f"{service}: its image must be ${{{variable}:?...}}, found {image}")
        elif image not in locked:
            problems.append(f"{service}: image {image} is not a locked linux/amd64 platform reference")
    missing = sorted(set(BUILT_SERVICES) - set(services))
    if missing:
        problems.append(f"services missing from the model: {', '.join(missing)}")
    return problems


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    commands = parser.add_subparsers(dest="command", required=True)
    derive_parser = commands.add_parser("derive")
    derive_parser.add_argument("--root", default=str(ROOT))
    derive_parser.add_argument("--out-dir", required=True)
    verify_parser = commands.add_parser("verify")
    verify_parser.add_argument("--dir", required=True)
    verify_parser.add_argument("--lock")
    args = parser.parse_args(argv)
    try:
        if args.command == "derive":
            built = derive(args.root, args.out_dir)
            print(json.dumps({"built": built}))
            return 0
        problems = verify(args.dir, load_lock(args.lock) if args.lock else None)
        for problem in problems:
            print(f"release-compose: {problem}", file=sys.stderr)
        return 1 if problems else 0
    except (ComposeError, OSError, ValueError, KeyError) as error:
        print(f"release-compose: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
