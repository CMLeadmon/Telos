#!/usr/bin/env python3
"""Verify that every image Telos builds from or runs is locked by registry digest.

    verify-image-lock.py --source [--root PATH]
    verify-image-lock.py --runtime [--candidate-lock PATH] [--root PATH]

``--source`` reads release/images.lock (a closed, versioned document) and checks it
against what the repository actually uses:

* every external ``FROM`` in a Dockerfile and every Compose ``image:`` must be
  ``registry/name:tag@sha256:<manifest digest>`` and map to exactly one lock entry
  with that same source and digest;
* local stage aliases (``FROM builder-base``) and declared ARG stage selections
  (``FROM builder-${EMBED_FRONTEND}``) are accepted only when the stage exists;
  any other dynamic or undeclared reference is rejected;
* the lock itself is well formed: no fabricated or repeating-character digests, no
  mutable ``latest``, only linux/amd64, no duplicate digest under different
  sources, and every ``images`` key must be used (a stale entry is as suspect as a
  missing one). ``toolImages`` and ``testImages`` are separate sections and never
  satisfy a production reference.

``--runtime`` inspects the effective containers and requires each third-party
container's image digest to be a locked manifest or platform digest. The runtime
``.Image`` of a container is a config ID, not a registry manifest digest, so the
comparison uses the manifest digest Podman records (``ImageDigest``) and, for
Telos-built images, the candidate lock. Nothing is ever refreshed here.

Exit codes: 0 verified, 1 a defect, 2 usage, 3 not_run (no container engine).
"""

import argparse
import json
import os
import re
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "lib"))
import schema_check  # noqa: E402

try:
    import yaml
except ImportError:  # pragma: no cover - PyYAML is a declared prerequisite
    raise SystemExit("verify-image-lock: PyYAML is required")

DOCKERFILES = ["backend/Dockerfile", "frontend/Dockerfile", "deploy/egress-proxy/Dockerfile"]
COMPOSE_FILES = ["docker-compose.yml", "docker-compose.test.yml"]
PINNED = re.compile(r"^(?P<source>[a-z0-9.-]+(?::[0-9]+)?/[a-z0-9._/-]+:[A-Za-z0-9][A-Za-z0-9._-]*)@(?P<digest>sha256:[0-9a-f]{64})$")
EXPAND = re.compile(r"\$\{([A-Za-z_][A-Za-z0-9_]*)\}")


class Defect(Exception):
    pass


def load_lock(root):
    path = root / "release" / "images.lock"
    try:
        document = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError) as error:
        raise Defect(f"release/images.lock is unreadable: {error}")
    schema = json.loads((Path(__file__).resolve().parent.parent / "release" / "images-lock.schema.json").read_text(encoding="utf-8"))
    schema_check.check_schema(schema)
    try:
        schema_check.validate(document, schema)
    except schema_check.ValidationError as error:
        raise Defect(f"release/images.lock violates its schema: {error}")
    seen = {}
    for section in ("images", "toolImages", "testImages"):
        for key, entry in document[section].items():
            for label, digest in [("manifestDigest", entry["manifestDigest"]), ("linux/amd64", entry["platforms"]["linux/amd64"])]:
                hexpart = digest.split(":", 1)[1]
                if len(set(hexpart)) <= 2:
                    raise Defect(f"{section}.{key} {label} is a fabricated placeholder digest")
            tag = entry["source"].rsplit(":", 1)[1]
            if tag == "latest":
                raise Defect(f"{section}.{key} is the mutable tag 'latest'")
            if not entry["source"].startswith(entry["registry"] + "/"):
                raise Defect(f"{section}.{key} source and registry disagree")
            other = seen.get(entry["manifestDigest"])
            if other and other[1] != entry["source"]:
                raise Defect(f"{section}.{key} shares a digest with unrelated {other[0]}")
            seen.setdefault(entry["manifestDigest"], (f"{section}.{key}", entry["source"]))
    return document


def dockerfile_references(root, relative):
    """Yield (line, raw reference, stage name) for each FROM, plus the declared stage set."""
    text = (root / relative).read_text(encoding="utf-8")
    stages, froms = set(), []
    args = {}
    for number, line in enumerate(text.splitlines(), 1):
        stripped = line.strip()
        arg = re.match(r"ARG\s+([A-Za-z_][A-Za-z0-9_]*)(?:=(\S+))?", stripped)
        if arg:
            args[arg.group(1)] = arg.group(2)
        match = re.match(r"FROM\s+(?:--platform=\S+\s+)?(\S+)(?:\s+AS\s+(\S+))?", stripped, re.I)
        if match:
            froms.append((number, match.group(1), match.group(2)))
            if match.group(2):
                stages.add(match.group(2))
    return froms, stages, args


def check_source(root, lock):
    used = {}
    errors = []
    by_pinned = {f"{e['source']}@{e['manifestDigest']}": key for key, e in lock["images"].items()}
    test_pinned = {f"{e['source']}@{e['manifestDigest']}": key for key, e in lock["testImages"].items()}

    def claim(reference, where):
        if reference in by_pinned:
            used.setdefault(by_pinned[reference], []).append(where)
            return
        match = PINNED.match(reference)
        if not match:
            if "@" not in reference:
                errors.append(f"{where}: {reference} is not pinned by digest")
            else:
                errors.append(f"{where}: {reference} is not a fully qualified registry/name:tag@sha256:digest")
        elif reference in test_pinned:
            errors.append(f"{where}: a test-only image ({test_pinned[reference]}) cannot satisfy a production reference")
        else:
            known = [k for k, e in lock["images"].items() if e["source"] == match.group("source")]
            if known:
                errors.append(f"{where}: digest differs from the lock for images.{known[0]}")
            else:
                errors.append(f"{where}: {reference} is not in the reviewed lock")

    for relative in DOCKERFILES:
        if not (root / relative).exists():
            continue
        froms, stages, args = dockerfile_references(root, relative)
        for number, reference, _ in froms:
            where = f"{relative}:{number}"
            if reference in stages:
                continue
            if "${" in reference:
                # Declared ARG selection: the expanded name must be a declared stage.
                escaped = re.escape(reference)
                regex = re.compile("^" + re.sub(r"\\\$\\\{[A-Za-z_][A-Za-z0-9_]*\\\}", ".+", escaped) + "$")
                if any(regex.match(stage) for stage in stages):
                    for name in EXPAND.findall(reference):
                        if name not in args:
                            errors.append(f"{where}: stage selection uses an undeclared ARG {name}")
                    continue
                errors.append(f"{where}: dynamic reference {reference} does not select a declared stage")
                continue
            claim(reference, where)

    for relative in COMPOSE_FILES:
        path = root / relative
        if not path.exists():
            continue
        document = yaml.safe_load(path.read_text(encoding="utf-8")) or {}
        for name, service in (document.get("services") or {}).items():
            image = service.get("image")
            if image is None:
                if "build" not in service:
                    errors.append(f"{relative}: service {name} has neither image nor build")
                continue
            if "${" in str(image):
                errors.append(f"{relative}: service {name} image {image} is dynamic")
                continue
            if relative == "docker-compose.test.yml":
                if image not in test_pinned:
                    errors.append(f"{relative}: service {name} image {image} is not a locked test image")
                continue
            claim(str(image), f"{relative}: service {name}")

    # A production lock entry nothing uses is stale or unreviewed drift.
    for key in lock["images"]:
        if key not in used:
            errors.append(f"images.{key} is locked but no Dockerfile or Compose service uses it")
    return used, errors


def run_runtime(root, lock, candidate_lock):
    import shutil
    engine = shutil.which("podman")
    if engine is None:
        print("verify-image-lock: not_run: podman is required for --runtime", file=sys.stderr)
        return 3
    names = subprocess.run([engine, "ps", "--format", "{{.Names}}"], capture_output=True, text=True, check=False).stdout.split()
    locked = {}
    for section in ("images", "toolImages"):
        for key, entry in lock[section].items():
            for digest in (entry["manifestDigest"], entry["platforms"]["linux/amd64"]):
                locked[digest] = f"{section}.{key}"
    candidate_images = {}
    if candidate_lock:
        document = json.loads(Path(candidate_lock).read_text(encoding="utf-8"))
        candidate_images = {v for k, v in document.get("artifacts", {}).items() if k.startswith("images/")}
    problems = []
    for name in sorted(n for n in names if n.startswith("telos-")):
        inspected = json.loads(subprocess.run([engine, "inspect", "--type", "container", name], capture_output=True, text=True, check=True).stdout)[0]
        image_digest = inspected.get("ImageDigest") or ""
        image_id = inspected.get("Image") or ""
        if image_digest in locked:
            continue
        if candidate_images and ("sha256:" + image_id) in candidate_images:
            continue
        problems.append(f"{name}: image {image_digest or image_id[:19]} is neither a locked third-party digest nor a candidate image")
    if problems:
        for problem in problems:
            print(f"verify-image-lock: {problem}", file=sys.stderr)
        return 1
    print(f"verify-image-lock: runtime ok: {len([n for n in names if n.startswith('telos-')])} containers match the lock")
    return 0


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--source", action="store_true")
    mode.add_argument("--runtime", action="store_true")
    parser.add_argument("--root", default=str(Path(__file__).resolve().parent.parent))
    parser.add_argument("--candidate-lock")
    args = parser.parse_args(argv)
    root = Path(args.root).resolve()
    try:
        lock = load_lock(root)
        if args.source:
            used, errors = check_source(root, lock)
            if errors:
                for error in errors:
                    print(f"verify-image-lock: {error}", file=sys.stderr)
                return 1
            print(f"verify-image-lock: source ok: {len(used)} production images map exactly to the lock")
            return 0
        return run_runtime(root, lock, args.candidate_lock)
    except Defect as error:
        print(f"verify-image-lock: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
