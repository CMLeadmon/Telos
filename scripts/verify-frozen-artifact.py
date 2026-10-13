#!/usr/bin/env python3
"""Verify a frozen candidate artifact for one catalog gate (never builds anything).

Runner contract: ``--candidate-lock PATH --result-out PATH`` are supplied by the
gate runner; the gate id comes from ``--gate-id`` and must equal the runner's
``TELOS_GATE_ID``. The result is a closed ``gate-result`` record plus an observed
artifact descriptor. Exit codes: 0 passed, 3 not_run (a prerequisite such as a
container runtime is unavailable), anything else failed.

Gates:
  static-client-image  the exact OCI archive of the static client
  desktop-builds       the initial Linux AppImage and its identity sidecar
"""

import argparse
import gzip
import io
import json
import os
import platform
import shutil
import subprocess
import sys
import tarfile
import tempfile
import urllib.request
from pathlib import Path

sys.dont_write_bytecode = True
sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "lib"))
import release_graph as rg  # noqa: E402

EXIT_FAILED = 1
EXIT_NOT_RUN = 3
CLIENT_IMAGE = "images/telos-client.oci.tar"
HTML_ROOT = "usr/share/nginx/html"
FORBIDDEN_NAMES = {".env", "node.env", "telos-secrets.json", "id_rsa", "id_ed25519"}
FORBIDDEN_SUFFIXES = (".map", ".pem", ".key", ".ts", ".tsx")
SECRET_ENV = ("PASSWORD", "TOKEN", "SECRET", "DATABASE", "REDIS", "STORAGE", "JELLYFIN", "GRIMMORY")
PODMAN_TIMEOUT = 120


class Finding(Exception):
    """An assertion about the frozen artifact failed."""


class Unavailable(Exception):
    """A prerequisite for the check is not present (reported as not_run)."""


def require(condition, message):
    if not condition:
        raise Finding(message)


# --------------------------------------------------------------- OCI archive

def read_oci_archive(path):
    """Return (manifest digest, config document, layer blobs) from an OCI archive."""
    blobs = {}
    with tarfile.open(path, "r:*") as archive:
        names = {member.name: member for member in archive.getmembers()}
        for member in names.values():
            if member.issym() or member.islnk() or member.name.startswith(("/", "../")) or ".." in Path(member.name).parts:
                raise Finding(f"unsafe archive member {member.name!r}")
            if member.isfile() and member.name.startswith("blobs/sha256/"):
                blobs[member.name.rsplit("/", 1)[1]] = archive.extractfile(member).read()
        require("oci-layout" in names and "index.json" in names, "archive is not an OCI image layout")
        index = json.loads(archive.extractfile(names["index.json"]).read())
    for name, data in blobs.items():
        require(rg.raw_sha256(data) == name, f"blob {name[:12]} does not match its digest")
    manifests = index.get("manifests", [])
    require(len(manifests) == 1, "the archive must contain exactly one image manifest")
    manifest_digest = manifests[0]["digest"]
    manifest = json.loads(blobs[manifest_digest.split(":", 1)[1]])
    require("manifests" not in manifest, "multi-platform image indexes are not accepted")
    config = json.loads(blobs[manifest["config"]["digest"].split(":", 1)[1]])
    layers = [blobs[layer["digest"].split(":", 1)[1]] for layer in manifest["layers"]]
    return manifest_digest, config, layers


def layer_paths(layer):
    data = layer
    if data[:2] == b"\x1f\x8b":
        data = gzip.decompress(data)
    with tarfile.open(fileobj=io.BytesIO(data), mode="r:") as archive:
        return [member.name.lstrip("./") for member in archive.getmembers()]


def check_static_client(candidate, source_commit, version):
    entry = candidate.artifact(CLIENT_IMAGE)
    require(entry["kind"] == "oci-image" and entry["platform"] == "linux/amd64", "client artifact must be a linux/amd64 OCI image")
    path = rg.resolve_regular(candidate.root, CLIENT_IMAGE)
    digest, config, layers = read_oci_archive(path)
    require(digest == entry["contentDigest"], "OCI manifest digest differs from the frozen artifact index")
    settings = config.get("config", {})
    user = str(settings.get("User", ""))
    require(user not in ("", "0", "root", "0:0", "root:root"), "client image must run as a non-root user")
    require(set(settings.get("ExposedPorts", {})) == {"8080/tcp"}, "client image must expose only 8080/tcp")
    for variable in settings.get("Env", []):
        name = variable.split("=", 1)[0].upper()
        require(not any(word in name for word in SECRET_ENV), f"client image sets secret-looking variable {name}")
    labels = settings.get("Labels", {})
    require(labels.get("org.opencontainers.image.revision") == source_commit, "image revision label differs from the candidate source")
    require(labels.get("org.opencontainers.image.version") == version, "image version label differs from the candidate version")
    paths = [path for layer in layers for path in layer_paths(layer)]
    html = [item for item in paths if item.startswith(HTML_ROOT + "/")]
    require(any(item == f"{HTML_ROOT}/index.html" for item in html), "client image has no exported index.html")
    for item in html:
        parts = Path(item).parts
        base = parts[-1]
        require(base not in FORBIDDEN_NAMES, f"client image contains {base}")
        require(not base.endswith(FORBIDDEN_SUFFIXES), f"client image contains {base}")
        require("node_modules" not in parts and "src" not in parts, f"client image contains source or dependencies: {item}")
    return entry, "client image static assertions passed"


def smoke_static_client(path):
    podman = shutil.which("podman")
    if podman is None:
        raise Unavailable("podman is required to load and run the exact client archive for the HTTP smoke")
    container = None
    try:
        loaded = subprocess.run([podman, "load", "-q", "-i", str(path)], capture_output=True, text=True, timeout=PODMAN_TIMEOUT, check=True)
        image = loaded.stdout.strip().splitlines()[-1].split()[-1]
        started = subprocess.run(
            [podman, "run", "-d", "--read-only", "--cap-drop=all", "--security-opt=no-new-privileges",
             "--tmpfs", "/var/cache/nginx:uid=101,gid=101,mode=0750", "--tmpfs", "/var/run:uid=101,gid=101,mode=0750",
             "-p", "127.0.0.1::8080", image],
            capture_output=True, text=True, timeout=PODMAN_TIMEOUT, check=True)
        container = started.stdout.strip()
        mapped = subprocess.run([podman, "port", container, "8080/tcp"], capture_output=True, text=True, timeout=30, check=True)
        port = mapped.stdout.strip().rsplit(":", 1)[1]
        body = urllib.request.urlopen(f"http://127.0.0.1:{port}/healthz", timeout=10).read()
        require(body.strip() == b"ok", "client /healthz did not answer ok")
        root = urllib.request.urlopen(f"http://127.0.0.1:{port}/", timeout=10).read()
        require(b"<html" in root.lower(), "client root did not serve exported HTML")
    except (subprocess.SubprocessError, OSError) as error:
        raise Finding(f"client image smoke failed: {type(error).__name__}") from error
    finally:
        if container:
            subprocess.run([podman, "rm", "-f", container], capture_output=True, timeout=60, check=False)


# --------------------------------------------------------------- desktop

def check_desktop(candidate, source_commit, version):
    desktop = [a for a in candidate.index["artifacts"] if a["kind"] == "desktop"]
    images = [a for a in desktop if a["path"].endswith(".AppImage")]
    sidecars = [a for a in desktop if a["path"].endswith(".AppImage.identity.json")]
    other = [a["path"] for a in desktop if a not in images and a not in sidecars]
    require(not other, f"unsupported desktop artifact in the initial candidate: {other[0] if other else ''}")
    require(len(images) == 1, f"exactly one Linux AppImage is required, found {len(images)}")
    require(len(sidecars) == 1, f"exactly one artifact identity sidecar is required, found {len(sidecars)}")
    image, sidecar = images[0], sidecars[0]
    require(image["platform"] == "linux-x86_64", "AppImage platform must be linux-x86_64")
    require(sidecar["path"] == image["path"] + ".identity.json", "identity sidecar does not belong to the AppImage")
    identity = rg.load_record("artifact-identity", rg.read_regular(candidate.root, sidecar["path"]), canonical=False)
    require(identity["sha256"] == image["sha256"], "identity sidecar sha256 differs from the AppImage")
    require(identity["size"] == image["size"], "identity sidecar size differs from the AppImage")
    require(identity["sourceCommit"] == source_commit, "identity sidecar source differs from the candidate")
    require(identity["version"] == version, "identity sidecar version differs from the candidate")
    require(version in image["path"], "AppImage name does not carry the candidate version")
    # Inspect, never execute: the bytes must carry the ELF magic of a Linux executable.
    with open(rg.resolve_regular(candidate.root, image["path"]), "rb") as handle:
        require(handle.read(4) == b"\x7fELF", "AppImage is not an ELF executable")
    return None, f"exactly one AppImage ({image['path']}) with a matching identity sidecar"


# --------------------------------------------------------------- result writing

def write_outputs(result_path, record, subject):
    out_dir = Path(result_path).parent
    attachments = []
    if subject is not None:
        subject_bytes = rg.canonical_bytes(subject)
        (out_dir / "subject.json").write_bytes(subject_bytes)
        attachments.append({"path": "subject.json", "sha256": rg.raw_sha256(subject_bytes), "kind": "subject"})
        record["subjectFile"] = "subject.json"
    record["attachments"] = attachments
    rg.validate_record("gate-result", record)
    rg.write_atomic_exclusive(result_path, rg.canonical_bytes(record))


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gate-id", required=True, choices=["static-client-image", "desktop-builds"])
    parser.add_argument("--candidate-lock", required=True)
    parser.add_argument("--result-out", required=True)
    parser.add_argument("--skip-runtime-smoke", action="store_true", help=argparse.SUPPRESS)
    arguments = parser.parse_args(argv)
    runner_gate = os.environ.get("TELOS_GATE_ID")
    if runner_gate not in (None, arguments.gate_id):
        print(f"verify-frozen-artifact: runner gate {runner_gate!r} differs from --gate-id", file=sys.stderr)
        return 2

    lock_bytes = Path(arguments.candidate_lock).read_bytes()
    lock_digest = rg.digest_bytes(lock_bytes)
    lock = json.loads(lock_bytes)
    source_commit, version = lock["sourceCommit"], lock["version"]
    record = {
        "schemaVersion": 1, "recordKind": "candidate", "gateId": arguments.gate_id,
        "sourceCommit": source_commit, "candidateLockDigest": lock_digest, "status": "passed",
        "reason": "", "subjectFile": None, "attachments": [],
        "toolVersions": {"python3": platform.python_version()},
    }
    status_code = 0
    subject = None
    try:
        root = os.path.dirname(os.path.abspath(arguments.candidate_lock))
        rg.verify_root(root)
        import gate_producers
        candidate = gate_producers.Candidate(arguments.candidate_lock, source_commit, lock_digest)
        require(candidate.full, f"candidate graph does not verify: {candidate.problem}")
        if arguments.gate_id == "static-client-image":
            entry, reason = check_static_client(candidate, source_commit, version)
            if not arguments.skip_runtime_smoke:
                smoke_static_client(rg.resolve_regular(root, CLIENT_IMAGE))
                reason += "; exact archive loaded and served /healthz"
            subject = {"schemaVersion": 1, "kind": "artifact", "sourceCommit": source_commit,
                       "candidateLockDigest": lock_digest, "selector": f"artifact:{CLIENT_IMAGE}",
                       "artifactDigest": "sha256:" + entry["sha256"]}
        else:
            _, reason = check_desktop(candidate, source_commit, version)
            subject = {"schemaVersion": 1, "kind": "artifact", "sourceCommit": source_commit,
                       "candidateLockDigest": lock_digest, "selector": "artifact-index",
                       "artifactDigest": rg.digest_bytes(rg.read_regular(root, "artifact-index.json"))}
        record["reason"] = reason
    except Unavailable as error:
        record.update(status="not_run", reason=str(error))
        status_code, subject = EXIT_NOT_RUN, None
    except (Finding, rg.GraphError, OSError, KeyError, json.JSONDecodeError, tarfile.TarError, ValueError) as error:
        record.update(status="failed", reason=str(error)[:500] or "artifact verification failed")
        status_code, subject = EXIT_FAILED, None
    # A failed result without a usable reason would be rejected; keep it sanitized and non-empty.
    if not record["reason"]:
        record["reason"] = "artifact verification produced no detail"
    try:
        write_outputs(arguments.result_out, record, subject)
    except (rg.GraphError, OSError) as error:
        print(f"verify-frozen-artifact: cannot write result: {error}", file=sys.stderr)
        return EXIT_FAILED
    print(f"{arguments.gate_id}: {record['status']}: {record['reason']}")
    return status_code


if __name__ == "__main__":
    sys.exit(main())
