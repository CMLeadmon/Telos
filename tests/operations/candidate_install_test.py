#!/usr/bin/env python3
"""Installing a candidate as a generation: verify, load images, copy immutably, keep the lock, optionally activate.

A candidate is produced by the real builder against the miniature repository (stand-in podman/skopeo);
the installer then runs against a temporary release base with a stand-in `podman load` that records
which manifests were loaded. Each rejection breaks one thing and expects nothing to have been installed."""

import contextlib
import io
import json
import os
import shutil
import stat
import sys
import tempfile
import unittest
from pathlib import Path

sys.dont_write_bytecode = True
HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
sys.path.insert(0, str(HERE.parents[1] / "scripts" / "lib"))
import build_release as br  # noqa: E402
import candidate_install as ci  # noqa: E402
import generations as gen  # noqa: E402
import release_graph as rg  # noqa: E402
from build_release_test import Fixture  # noqa: E402

PODMAN = '''#!/usr/bin/env python3
import json, os, sys, tarfile
state = os.environ["FAKE_PODMAN_LOADED"]
args = sys.argv[1:]
loaded = json.load(open(state)) if os.path.exists(state) else []
if args[0] == "load":
    with tarfile.open(args[2]) as tar:
        index = json.load(tar.extractfile("index.json"))
    loaded.append(index["manifests"][0]["digest"])
    json.dump(loaded, open(state, "w"))
elif args[0] == "tag":
    with open(state + ".tags", "a") as handle:
        handle.write(args[1] + " " + args[2] + "\\n")
elif args[:2] == ["image", "exists"]:
    digest = args[2].split("@", 1)[1]
    sys.exit(0 if digest in loaded and not os.environ.get("FAKE_PODMAN_FORGET") else 1)
else:
    sys.exit("fake podman: unsupported " + " ".join(args))
'''


class InstallFixture(Fixture):
    def setUp(self):
        super().setUp()
        self.build()
        self.candidate = self.dir / "out" / "candidate"
        self.base = self.dir / "srv"
        self.node = self.dir / "node"
        self.base.mkdir()
        self.node.mkdir()
        self.loaded = self.dir / "loaded.json"
        podman = self.dir / "podman"
        podman.write_text(PODMAN, encoding="utf-8")
        os.chmod(podman, 0o755)
        self.podman = str(podman)
        os.environ["FAKE_PODMAN_LOADED"] = str(self.loaded)
        os.environ.pop("FAKE_PODMAN_FORGET", None)
        self.addCleanup(lambda: [os.environ.pop(k, None) for k in ("FAKE_PODMAN_LOADED", "FAKE_PODMAN_FORGET")])
        self.lock_digest = rg.digest_bytes((self.candidate / "candidate-lock.json").read_bytes()).split(":")[1]

    def install(self, activate=False, candidate=None):
        return ci.install(str(candidate or self.candidate), str(self.base), str(self.node), activate, self.podman, owner_root=False)

    def generation(self):
        return self.base / "generations" / self.lock_digest


class InstallTests(InstallFixture):
    def test_a_candidate_installs_as_an_immutable_verified_generation(self):
        result = self.install()
        self.assertEqual(result["generation"], self.lock_digest)
        self.assertFalse(result["activated"])
        self.assertTrue(self.generation().is_dir())
        self.assertEqual(stat.S_IMODE(self.generation().stat().st_mode), 0o755)
        manifest, _ = gen.verify_generation(str(self.base), self.lock_digest, require_root_owner=False)
        self.assertEqual(manifest["version"], self.version)
        self.assertIsNone(gen.current_generation(str(self.base)))
        self.assertEqual(stat.S_IMODE((self.generation() / "images.env").stat().st_mode), 0o600)
        self.assertTrue(os.access(self.generation() / "telos", os.X_OK))

    def test_every_oci_archive_was_loaded_and_every_reference_resolves(self):
        result = self.install()
        loaded = json.loads(self.loaded.read_text())
        index = json.loads((self.candidate / "artifact-index.json").read_text())
        expected = sorted(a["contentDigest"] for a in index["artifacts"] if a["kind"] == "oci-image")
        self.assertEqual(sorted(loaded), expected)
        self.assertEqual(result["images"], 4)   # three built images plus redis in the miniature model

    def test_the_candidate_lock_is_kept_in_node_state(self):
        self.install()
        kept = self.node / "state" / "candidates" / self.lock_digest
        for name in rg.GRAPH_DOCUMENTS.values():
            self.assertEqual((kept / name).read_bytes(), (self.candidate / name).read_bytes())
        self.assertEqual(stat.S_IMODE((self.node / "state").stat().st_mode), 0o700)

    def test_activation_points_current_writes_the_receipt_and_the_current_lock(self):
        result = self.install(activate=True)
        self.assertTrue(result["activated"])
        self.assertEqual(gen.current_generation(str(self.base)), self.lock_digest)
        receipt = rg.load_record("installed-generation", (self.node / "state" / "installed-generation.json").read_bytes())
        self.assertEqual(receipt["candidateLockDigest"], "sha256:" + self.lock_digest)
        current_lock = (self.node / "state" / "current-candidate-lock.json").read_bytes()
        self.assertEqual(current_lock, (self.candidate / "candidate-lock.json").read_bytes())
        gen.check_receipt(str(self.base), str(self.node), self.lock_digest, require_root_owner=False)

    def test_installing_twice_keeps_the_generation_and_reloads_the_images(self):
        self.install()
        first = json.loads(self.loaded.read_text())
        second = self.install()
        self.assertTrue(second["alreadyInstalled"])
        self.assertEqual(len(json.loads(self.loaded.read_text())), 2 * len(first), "the images are loaded again: they may have been pruned or untagged")

    def test_every_loaded_image_is_pinned_under_a_name_no_later_candidate_reuses(self):
        self.install()
        pins = [line.split() for line in Path(str(self.loaded) + ".tags").read_text().splitlines()]
        self.assertEqual(len(pins), 4)
        for source, pin in pins:
            repository, digest = source.split("@")
            self.assertEqual(pin, f"{repository}:telos-{digest.split(':')[1][:12]}")

    def test_a_second_activation_is_refused(self):
        self.install(activate=True)
        with self.assertRaisesRegex(ci.InstallError, "already active"):
            self.install(activate=True)


class RejectionTests(InstallFixture):
    def assert_nothing_installed(self):
        self.assertFalse((self.base / "generations").exists() and any((self.base / "generations").iterdir()))
        self.assertFalse(self.loaded.exists())

    def test_a_tampered_payload_is_refused_before_anything_is_loaded(self):
        victim = self.candidate / "generation" / "scripts" / "backup.sh"
        victim.write_bytes(victim.read_bytes() + b"# tampered\n")
        with self.assertRaises(rg.GraphError):
            self.install()
        self.assert_nothing_installed()

    def test_a_tampered_image_archive_is_refused(self):
        archive = self.candidate / "images" / "telos-core.oci.tar"
        archive.write_bytes(archive.read_bytes() + b"x")
        with self.assertRaises(rg.GraphError):
            self.install()
        self.assert_nothing_installed()

    def test_an_extra_unindexed_file_is_refused(self):
        (self.candidate / "generation" / "extra.sh").write_text("#!/bin/sh\n")
        with self.assertRaises(rg.GraphError):
            self.install()
        self.assert_nothing_installed()

    def test_a_symlink_in_the_candidate_is_refused(self):
        os.symlink("/etc/passwd", self.candidate / "generation" / "link")
        with self.assertRaises(rg.GraphError):
            self.install()
        self.assert_nothing_installed()

    def test_an_image_that_does_not_resolve_after_loading_installs_nothing(self):
        os.environ["FAKE_PODMAN_FORGET"] = "1"
        with self.assertRaisesRegex(ci.InstallError, "does not resolve from local storage"):
            self.install()
        self.assertFalse((self.base / "generations").exists() and any((self.base / "generations").iterdir()))

    def test_an_existing_generation_that_was_modified_is_refused(self):
        self.install()
        victim = self.generation() / "scripts" / "backup.sh"
        os.chmod(victim, 0o755)
        victim.write_bytes(victim.read_bytes() + b"# edited in place\n")
        with self.assertRaises(gen.GenerationError):
            self.install()

    def test_nothing_is_left_behind_when_the_copy_fails(self):
        self.install()
        shutil.rmtree(self.generation())
        real = shutil.copyfile
        calls = {"n": 0}

        def flaky(src, dst, *a, **k):
            calls["n"] += 1
            if calls["n"] == 5:
                raise OSError("disk full")
            return real(src, dst, *a, **k)

        shutil.copyfile = flaky
        try:
            with self.assertRaises(OSError):
                self.install()
        finally:
            shutil.copyfile = real
        self.assertEqual([p.name for p in (self.base / "generations").iterdir()], [])


class CommandLineTests(InstallFixture):
    def test_the_command_line_prints_the_result_and_refuses_without_root(self):
        os.environ["TELOS_GENERATION_OWNER_CHECK"] = "0"
        self.addCleanup(os.environ.pop, "TELOS_GENERATION_OWNER_CHECK", None)
        with contextlib.redirect_stdout(io.StringIO()) as printed:
            code = ci.main(["install", "--candidate", str(self.candidate), "--release-base", str(self.base), "--node", str(self.node), "--podman", self.podman])
        self.assertEqual(code, 0)
        self.assertEqual(json.loads(printed.getvalue())["generation"], self.lock_digest)
        os.environ.pop("TELOS_GENERATION_OWNER_CHECK")
        if os.geteuid() != 0:
            with contextlib.redirect_stderr(io.StringIO()):
                self.assertEqual(ci.main(["install", "--candidate", str(self.candidate), "--release-base", str(self.base)]), 2)


if __name__ == "__main__":
    unittest.main()
