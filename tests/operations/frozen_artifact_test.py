#!/usr/bin/env python3
"""Mutation tests for scripts/verify-frozen-artifact.py (Phase 5 Task 4a).

The adapter verifies exact frozen bytes. These tests build a tiny candidate with a
synthetic OCI archive and AppImage, run the adapter the way the runner does, and
require every substitution, weakening and rebuild attempt to be rejected.
"""
import gzip
import hashlib
import io
import json
import os
import stat
import subprocess
import sys
import tarfile
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts/lib"))
import release_graph as rg  # noqa: E402

ADAPTER = str(ROOT / "scripts/verify-frozen-artifact.py")
COMMIT = "a" * 40
VERSION = "0.1.0-beta.1"
CLIENT = "images/telos-client.oci.tar"
APPIMAGE = f"desktop/telos-{VERSION}-x86_64.AppImage"
ELF = b"\x7fELF" + b"\0" * 60


def sha(data):
    return hashlib.sha256(data).hexdigest()


def layer_tar(files):
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode="w") as archive:
        for name, data in files.items():
            info = tarfile.TarInfo(name)
            info.size = len(data)
            archive.addfile(info, io.BytesIO(data))
    return gzip.compress(buffer.getvalue(), mtime=0)


def oci_archive(user="101:101", ports=("8080/tcp",), env=(), labels=None, files=None, corrupt_blob=False, symlink=False):
    labels = {"org.opencontainers.image.revision": COMMIT, "org.opencontainers.image.version": VERSION} if labels is None else labels
    files = {"usr/share/nginx/html/index.html": b"<html></html>", "usr/share/nginx/html/_next/a.js": b"x"} if files is None else files
    layer = layer_tar(files)
    config = json.dumps({"config": {"User": user, "ExposedPorts": {p: {} for p in ports}, "Env": list(env),
                                    "Labels": labels}}).encode()
    manifest = json.dumps({"schemaVersion": 2, "config": {"digest": "sha256:" + sha(config), "size": len(config)},
                           "layers": [{"digest": "sha256:" + sha(layer), "size": len(layer)}]}).encode()
    index = json.dumps({"schemaVersion": 2, "manifests": [{"digest": "sha256:" + sha(manifest), "size": len(manifest)}]}).encode()
    blobs = {sha(config): config, sha(layer): layer, sha(manifest): manifest}
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode="w") as archive:
        for name, data in {"oci-layout": b'{"imageLayoutVersion":"1.0.0"}', "index.json": index}.items():
            info = tarfile.TarInfo(name)
            info.size = len(data)
            archive.addfile(info, io.BytesIO(data))
        for digest, data in blobs.items():
            if corrupt_blob and digest == sha(layer):
                data = data + b"tamper"
            info = tarfile.TarInfo(f"blobs/sha256/{digest}")
            info.size = len(data)
            archive.addfile(info, io.BytesIO(data))
        if symlink:
            info = tarfile.TarInfo("blobs/link")
            info.type = tarfile.SYMTYPE
            info.linkname = "/etc/passwd"
            archive.addfile(info)
    return buffer.getvalue(), "sha256:" + sha(manifest)


def identity(data, **overrides):
    value = {"schemaVersion": 1, "sourceCommit": COMMIT, "version": VERSION, "platform": "linux-x86_64",
             "size": len(data), "sha256": sha(data), "workflowRunId": 11, "workflowRunAttempt": 1}
    value.update(overrides)
    return value


def build_candidate(base, archive=None, content_digest=None, desktop=None, extra_artifacts=()):
    root = Path(base) / "candidate"
    archive, digest = archive if archive else oci_archive()
    desktop = [(APPIMAGE, ELF, identity(ELF))] if desktop is None else desktop
    payload = [(CLIENT, archive, "oci-image", "linux/amd64", content_digest or digest)]
    for path, data, record in desktop:
        payload.append((path, data, "desktop", "linux-x86_64", None))
        if record is not None:
            payload.append((path + ".identity.json", rg.canonical_bytes(record), "desktop", "linux-x86_64", None))
    payload += list(extra_artifacts)
    descriptors = []
    for path, data, kind, platform_name, content in payload:
        target = root / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)
        descriptors.append({"path": path, "kind": kind, "platform": platform_name, "contentDigest": content})
    index = rg.build_index(root, {"artifacts": descriptors})
    rg.write_atomic_exclusive(root / "artifact-index.json", rg.canonical_bytes(index))
    manifest = rg.build_manifest(root, {
        "version": VERSION, "sourceCommit": COMMIT, "sourceTag": "v" + VERSION, "sourceDateEpoch": 1787234400,
        "imagesLockDigest": "sha256:" + "1" * 64, "migrationManifestDigest": "sha256:" + "2" * 64,
        "buildInputs": {"npmLock": "sha256:" + "3" * 64, "cargoLock": "sha256:" + "4" * 64, "goSum": "sha256:" + "5" * 64}})
    rg.write_atomic_exclusive(root / "release-manifest.json", rg.canonical_bytes(manifest))
    rg.write_atomic_exclusive(root / "candidate-lock.json", rg.canonical_bytes(rg.build_candidate_lock(root)))
    return root


class AdapterCase(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.runs = 0

    def run_adapter(self, root, gate_id, *extra, path=None, env_gate=True):
        self.runs += 1
        out = self.base / f"out-{self.runs}"
        out.mkdir()
        environment = dict(os.environ)
        if env_gate:
            environment["TELOS_GATE_ID"] = gate_id
        if path is not None:
            environment["PATH"] = path
        done = subprocess.run(
            [sys.executable, "-B", ADAPTER, "--gate-id", gate_id, "--candidate-lock", str(root / "candidate-lock.json"),
             "--result-out", str(out / "result.json"), *extra],
            capture_output=True, text=True, env=environment)
        self.out = out
        result = rg.verify_result_directory(out, "result.json") if (out / "result.json").exists() else None
        return done, result

    def assertFailed(self, root, gate_id, fragment, *extra):
        done, result = self.run_adapter(root, gate_id, *extra)
        self.assertEqual(done.returncode, 1, done.stdout + done.stderr)
        self.assertEqual(result["status"], "failed")
        self.assertIn(fragment, result["reason"])
        self.assertIsNone(result["subjectFile"])
        return result


class StaticClientTests(AdapterCase):
    def test_valid_archive_passes_static_checks_and_binds_the_exact_bytes(self):
        root = build_candidate(self.base)
        done, result = self.run_adapter(root, "static-client-image", "--skip-runtime-smoke")
        self.assertEqual(done.returncode, 0, done.stdout + done.stderr)
        self.assertEqual(result["status"], "passed")
        subject = rg.parse_canonical((self.out / "subject.json").read_bytes())
        self.assertEqual(subject["selector"], f"artifact:{CLIENT}")
        self.assertEqual(subject["artifactDigest"], "sha256:" + sha((root / CLIENT).read_bytes()))

    def test_manifest_digest_must_equal_the_frozen_index(self):
        root = build_candidate(self.base, content_digest="sha256:" + "f" * 64)
        self.assertFailed(root, "static-client-image", "manifest digest differs", "--skip-runtime-smoke")

    def test_substituted_archive_after_freezing_fails(self):
        root = build_candidate(self.base)
        (root / CLIENT).write_bytes(oci_archive(user="0")[0])
        self.assertFailed(root, "static-client-image", "differ", "--skip-runtime-smoke")

    def test_image_must_be_present_in_the_index(self):
        root = build_candidate(self.base)
        index = json.loads((root / "artifact-index.json").read_text())
        index["artifacts"] = [a for a in index["artifacts"] if a["path"] != CLIENT]
        (root / "artifact-index.json").write_bytes(rg.canonical_bytes(index))
        self.assertFailed(root, "static-client-image", "does not name the artifact index", "--skip-runtime-smoke")

    def test_runtime_identity_weakenings_are_rejected(self):
        cases = {
            "root user": (dict(user="0"), "non-root"),
            "empty user": (dict(user=""), "non-root"),
            "extra port": (dict(ports=("8080/tcp", "9000/tcp")), "only 8080/tcp"),
            "secret env": (dict(env=("DATABASE_URL=x",)), "secret-looking"),
            "missing revision": (dict(labels={"org.opencontainers.image.version": VERSION}), "revision label"),
            "wrong version": (dict(labels={"org.opencontainers.image.revision": COMMIT, "org.opencontainers.image.version": "9.9.9-beta.1"}), "version label"),
        }
        for name, (options, fragment) in cases.items():
            with self.subTest(name):
                root = build_candidate(self.base / name.replace(" ", "-"), archive=oci_archive(**options))
                self.assertFailed(root, "static-client-image", fragment, "--skip-runtime-smoke")

    def test_forbidden_content_is_rejected(self):
        base = {"usr/share/nginx/html/index.html": b"<html></html>"}
        for name, extra in {
            "env file": {"usr/share/nginx/html/.env": b"x"},
            "node env": {"usr/share/nginx/html/node.env": b"x"},
            "source map": {"usr/share/nginx/html/app.js.map": b"x"},
            "dependencies": {"usr/share/nginx/html/node_modules/a/index.js": b"x"},
            "typescript": {"usr/share/nginx/html/app.ts": b"x"},
            "private key": {"usr/share/nginx/html/server.pem": b"x"},
            "source tree": {"usr/share/nginx/html/src/main.js": b"x"},
        }.items():
            with self.subTest(name):
                root = build_candidate(self.base / name.replace(" ", "-"), archive=oci_archive(files={**base, **extra}))
                self.assertFailed(root, "static-client-image", "contains", "--skip-runtime-smoke")
        root = build_candidate(self.base / "no-index", archive=oci_archive(files={"usr/share/nginx/html/a.js": b"x"}))
        self.assertFailed(root, "static-client-image", "no exported index.html", "--skip-runtime-smoke")

    def test_corrupt_blob_and_symlink_members_are_rejected(self):
        root = build_candidate(self.base / "corrupt", archive=oci_archive(corrupt_blob=True))
        self.assertFailed(root, "static-client-image", "does not match its digest", "--skip-runtime-smoke")
        root = build_candidate(self.base / "link", archive=oci_archive(symlink=True))
        self.assertFailed(root, "static-client-image", "unsafe archive member", "--skip-runtime-smoke")

    def test_missing_container_runtime_is_not_run_not_passed(self):
        root = build_candidate(self.base)
        done, result = self.run_adapter(root, "static-client-image", path="/nonexistent")
        self.assertEqual(done.returncode, 3, done.stdout + done.stderr)
        self.assertEqual(result["status"], "not_run")
        self.assertIn("podman", result["reason"])

    def test_gate_id_must_match_the_runner(self):
        root = build_candidate(self.base)
        self.runs += 1
        done = subprocess.run(
            [sys.executable, ADAPTER, "--gate-id", "static-client-image", "--candidate-lock", str(root / "candidate-lock.json"),
             "--result-out", str(self.base / "x.json")],
            capture_output=True, text=True, env=dict(os.environ, TELOS_GATE_ID="desktop-builds"))
        self.assertEqual(done.returncode, 2)
        self.assertFalse((self.base / "x.json").exists())

    def test_smoke_loads_and_runs_but_never_builds(self):
        calls = self.base / "podman-calls.txt"
        server = HTTPServer(("127.0.0.1", 0), type("Health", (BaseHTTPRequestHandler,), {
            "do_GET": lambda handler: (handler.send_response(200), handler.end_headers(),
                                       handler.wfile.write(b"ok" if handler.path == "/healthz" else b"<html></html>")),
            "log_message": lambda *a: None}))
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.shutdown)
        fake = self.base / "bin"
        fake.mkdir()
        podman = fake / "podman"
        podman.write_text(f"""#!/bin/sh
echo "$@" >> {calls}
case "$1" in
  load) echo "Loaded image: localhost/telos-client:test" ;;
  run) echo container123 ;;
  port) echo "127.0.0.1:{server.server_port}" ;;
  rm) ;;
  *) exit 9 ;;
esac
""")
        podman.chmod(0o755)
        root = build_candidate(self.base)
        done, result = self.run_adapter(root, "static-client-image", path=f"{fake}:{os.environ['PATH']}")
        self.assertEqual(done.returncode, 0, done.stdout + done.stderr)
        self.assertIn("served /healthz", result["reason"])
        subcommands = [line.split()[0] for line in calls.read_text().splitlines()]
        self.assertEqual(subcommands, ["load", "run", "port", "rm"])
        self.assertNotIn("build", subcommands)
        self.assertIn("--read-only", calls.read_text())


class DesktopTests(AdapterCase):
    def test_exactly_one_appimage_with_matching_identity_passes(self):
        root = build_candidate(self.base)
        done, result = self.run_adapter(root, "desktop-builds")
        self.assertEqual(done.returncode, 0, done.stdout + done.stderr)
        subject = rg.parse_canonical((self.out / "subject.json").read_bytes())
        self.assertEqual(subject["selector"], "artifact-index")
        self.assertEqual(subject["artifactDigest"], rg.digest_bytes((root / "artifact-index.json").read_bytes()))

    def test_absence_of_windows_and_macos_artifacts_is_valid(self):
        root = build_candidate(self.base)
        index = json.loads((root / "artifact-index.json").read_text())
        self.assertFalse([a for a in index["artifacts"] if a["path"].endswith((".exe", ".dmg", ".msi"))])
        done, _ = self.run_adapter(root, "desktop-builds")
        self.assertEqual(done.returncode, 0)

    def test_missing_or_duplicate_appimage(self):
        root = build_candidate(self.base / "none", desktop=[])
        self.assertFailed(root, "desktop-builds", "exactly one Linux AppImage")
        second = (f"desktop/telos-{VERSION}-second.AppImage", ELF + b"2", identity(ELF + b"2"))
        root = build_candidate(self.base / "two", desktop=[(APPIMAGE, ELF, identity(ELF)), second])
        self.assertFailed(root, "desktop-builds", "exactly one Linux AppImage")

    def test_sidecar_substitution_is_rejected(self):
        for name, record, fragment in [
            ("missing", None, "exactly one artifact identity sidecar"),
            ("wrong hash", identity(ELF, sha256="0" * 64), "sha256 differs"),
            ("wrong size", identity(ELF, size=1), "size differs"),
            ("wrong commit", identity(ELF, sourceCommit="b" * 40), "source differs"),
            ("wrong version", identity(ELF, version="0.1.0-beta.9"), "version differs"),
        ]:
            with self.subTest(name):
                root = build_candidate(self.base / name.replace(" ", "-"), desktop=[(APPIMAGE, ELF, record)])
                self.assertFailed(root, "desktop-builds", fragment)

    def test_non_elf_and_deferred_platform_artifacts_are_rejected(self):
        root = build_candidate(self.base / "text", desktop=[(APPIMAGE, b"#!/bin/sh\n", identity(b"#!/bin/sh\n"))])
        self.assertFailed(root, "desktop-builds", "not an ELF")
        windows = ("desktop/telos-setup.exe", b"MZ", None)
        root = build_candidate(self.base / "windows", desktop=[(APPIMAGE, ELF, identity(ELF))],
                               extra_artifacts=[(windows[0], windows[1], "desktop", "linux-x86_64", None)])
        self.assertFailed(root, "desktop-builds", "unsupported desktop artifact")

    def test_adapter_does_not_execute_the_input(self):
        marker = self.base / "executed"
        script = f"#!/bin/sh\ntouch {marker}\n".encode()
        root = build_candidate(self.base / "exec", desktop=[(APPIMAGE, script, identity(script))])
        self.run_adapter(root, "desktop-builds")
        self.assertFalse(marker.exists())


if __name__ == "__main__":
    unittest.main(argv=sys.argv)
