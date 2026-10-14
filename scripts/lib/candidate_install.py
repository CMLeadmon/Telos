#!/usr/bin/env python3
"""Install a verified candidate as an immutable generation on this node.

    candidate_install.py install --candidate DIR [--release-base /srv/telos] [--node /var/lib/telos]
                                 [--activate] [--podman podman]

Only the candidate's own bytes are used. In order: the whole release graph is verified; every OCI
archive is checked against the artifact index and loaded into local storage, and every image the
generation will run is proven to resolve by its digest with no registry; the generation tree is copied
into place root-owned and immutable, then verified against its own generation.json; the candidate lock
is kept in node state (it is how upgrade.sh and backup.sh name the candidate). `--activate` is only for
a node that has no active generation yet: it points `current` at the generation and writes the
installed receipt. Moving between generations is upgrade.sh's job, never this script's.
"""

import argparse
import json
import os
import re
import secrets
import shutil
import stat
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import build_release as br  # noqa: E402
import generations as gen  # noqa: E402
import release_graph as rg  # noqa: E402

IMAGE_REF = re.compile(r"^\s*image:\s*(\S+)\s*$", re.M)
DIGEST_REF = re.compile(r"[a-z0-9./_-]+@sha256:[0-9a-f]{64}\Z")


class InstallError(Exception):
    pass


def read_bytes(*parts):
    with open(os.path.join(*parts), "rb") as handle:
        return handle.read()


def fail(message):
    raise InstallError(message)


def required_image_refs(candidate):
    """Every digest-addressed image the generation will run (images.env plus its Compose model's third-party references)."""
    try:
        return gen.image_refs(os.path.join(candidate, "generation"))
    except gen.GenerationError as error:
        fail(str(error))


def pinned_name(ref_name, digest):
    """`localhost/telos-core:0.1.0-beta.1` + a manifest digest -> `localhost/telos-core:telos-<12 hex>`, a tag no later candidate reuses."""
    repository = ref_name.rsplit(":", 1)[0] if ":" in ref_name.rsplit("/", 1)[-1] else ref_name
    return f"{repository}:telos-{digest.split(':', 1)[1][:12]}", repository


def load_images(candidate, index, podman, run=subprocess.run):
    """podman load every indexed OCI archive after re-checking it against the index, pin each image under a name of its own,
    and prove every reference the generation uses resolves.

    Loading moves a repository:tag onto the new image, so an older generation's image (same tag, other digest) would lose its
    only name and stop resolving by digest. The pinned per-digest tag keeps every installed generation's images resolvable, and
    reloading is always safe: it also restores images that were pruned since the generation was installed.
    """
    for artifact in index["artifacts"]:
        if artifact["kind"] != "oci-image":
            continue
        path = os.path.join(candidate, artifact["path"])
        info = br.read_oci_archive(__import__("pathlib").Path(path))
        if info["manifestDigest"] != artifact["contentDigest"]:
            fail(f"{artifact['path']}: the archive's manifest is not the digest the index records")
        loaded = run([podman, "load", "-i", path], capture_output=True, text=True, check=False)
        if loaded.returncode != 0:
            fail(f"podman could not load {artifact['path']}: {(loaded.stderr or loaded.stdout).strip()[-200:]}")
        if not info.get("refName"):
            fail(f"{artifact['path']}: the archive names no image to pin")
        pin, repository = pinned_name(info["refName"], info["manifestDigest"])
        tagged = run([podman, "tag", f"{repository}@{info['manifestDigest']}", pin], capture_output=True, text=True, check=False)
        if tagged.returncode != 0:
            fail(f"podman could not pin {artifact['path']} as {pin}: {(tagged.stderr or tagged.stdout).strip()[-200:]}")
    for ref in required_image_refs(candidate):
        exists = run([podman, "image", "exists", ref], capture_output=True, check=False)
        if exists.returncode != 0:
            fail(f"after loading, {ref} does not resolve from local storage")


def copy_generation(candidate, release_base, generation_id, owner_root):
    """Copy generation/ into generations/<id> atomically: modes normalized, root-owned, no symlinks."""
    source = os.path.join(candidate, "generation")
    target_parent = os.path.join(release_base, "generations")
    os.makedirs(target_parent, mode=0o755, exist_ok=True)
    final = os.path.join(target_parent, generation_id)
    if os.path.lexists(final):
        fail(f"generation {generation_id[:12]} already exists; installation never overwrites a generation")
    staging = os.path.join(target_parent, f".incoming-{generation_id[:12]}-{secrets.token_hex(4)}")
    try:
        os.mkdir(staging, 0o755)
        for current, directories, files in os.walk(source):
            relative = os.path.relpath(current, source)
            destination = staging if relative == "." else os.path.join(staging, relative)
            os.makedirs(destination, mode=0o755, exist_ok=True)
            for name in sorted(files):
                origin = os.path.join(current, name)
                if os.path.islink(origin):
                    fail(f"{os.path.join(relative, name)} is a symbolic link")
                mode = os.lstat(origin).st_mode
                if not stat.S_ISREG(mode):
                    fail(f"{os.path.join(relative, name)} is not a regular file")
                shutil.copyfile(origin, os.path.join(destination, name))
                keep = 0o600 if name == "images.env" and relative == "." else (0o755 if mode & 0o111 else 0o644)
                os.chmod(os.path.join(destination, name), keep)
            directories.sort()
        if owner_root:
            for current, directories, files in os.walk(staging):
                os.chown(current, 0, 0)
                for name in files:
                    os.chown(os.path.join(current, name), 0, 0)
        os.chmod(staging, 0o755)
        gen.fsync_directory(staging)
        os.rename(staging, final)
        gen.fsync_directory(target_parent)
    except BaseException:
        shutil.rmtree(staging, ignore_errors=True)
        raise
    return final


def keep_candidate_records(candidate, node, generation_id):
    """The candidate lock and its graph documents, in node state; they name the installed candidate for upgrade, backup and restore."""
    state = os.path.join(node, "state")
    for parent in (state, os.path.join(state, "candidates")):
        if not os.path.isdir(parent):
            os.mkdir(parent, 0o700)
    directory = os.path.join(state, "candidates", generation_id)
    os.makedirs(directory, mode=0o700, exist_ok=True)
    for name in rg.GRAPH_DOCUMENTS.values():
        data = read_bytes(candidate, name)
        gen.atomic_replace(os.path.join(directory, name), data, mode=0o644)
    return directory


def install(candidate, release_base, node, activate, podman, owner_root, run=subprocess.run):
    verified = rg.verify_root(candidate)
    generation_id = verified["candidateLockDigest"].split(":", 1)[1]
    lock_bytes = read_bytes(candidate, rg.GRAPH_DOCUMENTS["candidate-lock"])
    if rg.raw_sha256(lock_bytes) != generation_id:
        fail("the candidate lock digest is not the digest of its bytes")
    index = rg.load_record("artifact-index", read_bytes(candidate, rg.GRAPH_DOCUMENTS["artifact-index"]))
    manifest = rg.load_record("generation-manifest", read_bytes(candidate, "generation", "generation.json"))
    if manifest["sourceCommit"] != verified["sourceCommit"] or manifest["version"] != verified["version"]:
        fail("generation.json describes a different source or version than the candidate")
    already = os.path.isdir(os.path.join(release_base, "generations", generation_id))
    load_images(candidate, index, podman, run)   # every time: it also restores images pruned or untagged since the first install
    if not already:
        copy_generation(candidate, release_base, generation_id, owner_root)
    gen.verify_generation(release_base, generation_id, require_root_owner=owner_root)
    keep_candidate_records(candidate, node, generation_id)
    activated = False
    if activate:
        if gen.current_generation(release_base) is not None:
            fail("a generation is already active; use upgrade.sh to move between generations")
        gen.activate(release_base, generation_id)
        gen.write_receipt(release_base, node, generation_id, require_root_owner=owner_root)
        activated = True
    return {"generation": generation_id, "path": os.path.join(release_base, "generations", generation_id), "version": verified["version"],
            "sourceCommit": verified["sourceCommit"], "alreadyInstalled": already, "activated": activated,
            "images": len(required_image_refs(candidate))}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    commands = parser.add_subparsers(dest="command", required=True)
    install_parser = commands.add_parser("install")
    install_parser.add_argument("--candidate", required=True)
    install_parser.add_argument("--release-base", default=os.environ.get("TELOS_RELEASE_BASE", "/srv/telos"))
    install_parser.add_argument("--node", default=os.environ.get("TELOS_NODE_PATH", "/var/lib/telos"))
    install_parser.add_argument("--activate", action="store_true")
    install_parser.add_argument("--podman", default=os.environ.get("TELOS_INSTALL_PODMAN", "podman"))
    args = parser.parse_args(argv)
    owner_root = os.environ.get("TELOS_GENERATION_OWNER_CHECK", "1") != "0"
    if owner_root and os.geteuid() != 0:
        print("install-candidate: run as root (the generation is root-owned and the images load into rootful Podman)", file=sys.stderr)
        return 2
    try:
        result = install(args.candidate, args.release_base, args.node, args.activate, args.podman, owner_root)
    except (InstallError, gen.GenerationError, rg.GraphError, br.BuildError, OSError, KeyError, ValueError) as error:
        print(f"install-candidate: {error}", file=sys.stderr)
        return 1
    print(json.dumps(result, sort_keys=True))
    return 0


if __name__ == "__main__":
    sys.exit(main())
