#!/usr/bin/env python3
"""The derived release model: no build keys, every third-party image locked to its platform manifest,
every self-built image required from images.env. Each test breaks one input and expects a refusal."""

import json
import os
import shutil
import sys
import tempfile
import unittest
from pathlib import Path

sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts" / "lib"))
import release_compose as rc  # noqa: E402


class Fixture(unittest.TestCase):
    def setUp(self):
        self.dir = Path(tempfile.mkdtemp(prefix="release-compose-"))
        self.addCleanup(shutil.rmtree, self.dir, True)
        self.src = self.dir / "src"
        (self.src / "release").mkdir(parents=True)
        for name in (*rc.BASE_FILES, rc.OVERLAY):
            shutil.copy(ROOT / name, self.src / name)
        shutil.copy(ROOT / "release" / "images.lock", self.src / "release" / "images.lock")
        self.out = self.dir / "out"

    def derive(self):
        return rc.derive(self.src, self.out)

    def edit(self, name, old, new, count=1):
        path = self.src / name
        text = path.read_text(encoding="utf-8")
        self.assertIn(old, text)
        path.write_text(text.replace(old, new, count), encoding="utf-8")


class DeriveTests(Fixture):
    def test_the_real_model_derives_and_verifies(self):
        built = self.derive()
        self.assertEqual(set(built), set(rc.BUILT_SERVICES))
        self.assertEqual(rc.verify(self.out), [])

    def test_no_build_key_and_no_tag_survive_in_derived_files(self):
        self.derive()
        for name in rc.BASE_FILES:
            text = (self.out / name).read_text(encoding="utf-8")
            self.assertNotIn("build:", text)
            self.assertNotIn("dockerfile:", text)
        base = (self.out / "docker-compose.yml").read_text(encoding="utf-8")
        for line in base.splitlines():
            if line.startswith("    image: "):
                self.assertRegex(line, r"^    image: [a-z0-9./-]+@sha256:[0-9a-f]{64}$")

    def test_comments_outside_build_blocks_survive(self):
        self.derive()
        self.assertIn("One-shot schema-owner migrator", (self.out / "docker-compose.yml").read_text(encoding="utf-8"))

    def test_third_party_images_become_the_locked_platform_digest(self):
        self.derive()
        lock = json.loads((ROOT / "release" / "images.lock").read_text(encoding="utf-8"))
        base = (self.out / "docker-compose.yml").read_text(encoding="utf-8")
        for name in ("redis", "postgres", "jellyfin", "traefik", "prometheus", "mariadb", "grimmory", "clamav"):
            platform = lock["images"][name]["platforms"]["linux/amd64"]
            self.assertIn("@" + platform, base, name)
            self.assertNotIn(lock["images"][name]["manifestDigest"], base, name)

    def test_an_unlocked_third_party_image_is_refused(self):
        self.edit("docker-compose.yml", "docker.io/library/redis:7-alpine@", "docker.io/library/redis:8-alpine@")
        with self.assertRaisesRegex(rc.ComposeError, "not in release/images.lock"):
            self.derive()

    def test_a_new_built_service_without_an_overlay_entry_is_refused(self):
        self.edit("docker-compose.yml", "  clamav:\n", "  rogue:\n    build:\n      context: .\n    container_name: rogue\n\n  clamav:\n")
        with self.assertRaisesRegex(rc.ComposeError, "release overlay covers"):
            self.derive()

    def test_an_overlay_entry_for_a_service_that_no_longer_builds_is_refused(self):
        self.edit("docker-compose.yml", "  telos-client:\n    build:\n      context: ./frontend\n      dockerfile: Dockerfile\n",
                  "  telos-client:\n    image: docker.io/library/redis:7-alpine@sha256:858f009f9709ce576febc734aa78b8f6d624b82571f9ddb6bda4377c833b3499\n")
        with self.assertRaisesRegex(rc.ComposeError, "release overlay covers"):
            self.derive()

    def test_a_missing_overlay_is_refused(self):
        (self.src / rc.OVERLAY).unlink()
        with self.assertRaises((rc.ComposeError, OSError)):
            self.derive()


class VerifyTests(Fixture):
    def setUp(self):
        super().setUp()
        self.derive()

    def rewrite(self, name, old, new):
        path = self.out / name
        text = path.read_text(encoding="utf-8")
        self.assertIn(old, text)
        path.write_text(text.replace(old, new, 1), encoding="utf-8")

    def test_a_surviving_build_key_fails(self):
        self.rewrite("docker-compose.yml", "    container_name: telos-core\n", "    container_name: telos-core\n    build:\n      context: .\n")
        self.assertTrue(any("build" in problem for problem in rc.verify(self.out)))

    def test_a_service_image_that_is_not_locked_fails(self):
        lock = json.loads((ROOT / "release" / "images.lock").read_text(encoding="utf-8"))
        digest = lock["images"]["redis"]["platforms"]["linux/amd64"]
        self.rewrite("docker-compose.yml", "@" + digest, "@sha256:" + "0" * 64)
        self.assertTrue(any("not a locked linux/amd64 platform reference" in problem for problem in rc.verify(self.out)))

    def test_a_mutable_tag_fails(self):
        lock = json.loads((ROOT / "release" / "images.lock").read_text(encoding="utf-8"))
        digest = lock["images"]["redis"]["platforms"]["linux/amd64"]
        self.rewrite("docker-compose.yml", "docker.io/library/redis@" + digest, "docker.io/library/redis:7-alpine")
        self.assertTrue(rc.verify(self.out))

    def test_an_overlay_that_hardcodes_an_image_fails(self):
        self.rewrite(rc.OVERLAY, "${TELOS_CLIENT_IMAGE:?the candidate client image is required (images.env)}", "localhost/telos-client:latest")
        self.assertTrue(any("telos-client" in problem for problem in rc.verify(self.out)))

    def test_an_overlay_that_does_not_require_the_variable_fails(self):
        self.rewrite(rc.OVERLAY, "${TELOS_CORE_IMAGE:?the candidate core image is required (images.env)}", "${TELOS_CORE_IMAGE}")
        self.assertTrue(rc.verify(self.out))

    def test_the_wrong_variable_for_a_service_fails(self):
        self.rewrite(rc.OVERLAY, "${TELOS_CLIENT_IMAGE:?the candidate client image is required (images.env)}", "${TELOS_CORE_IMAGE:?the candidate core image is required (images.env)}")
        self.assertTrue(any("TELOS_CLIENT_IMAGE" in problem for problem in rc.verify(self.out)))

    def test_a_missing_overlay_service_leaves_it_without_an_image(self):
        text = (self.out / rc.OVERLAY).read_text(encoding="utf-8")
        marker = "  telos-egress-proxy:\n"
        self.assertIn(marker, text)
        (self.out / rc.OVERLAY).write_text(text[: text.index(marker)], encoding="utf-8")
        self.assertTrue(any("telos-egress-proxy" in problem and "no image" in problem for problem in rc.verify(self.out)))

    def test_a_missing_file_is_reported(self):
        (self.out / "docker-compose.setup.yml").unlink()
        self.assertTrue(rc.verify(self.out))


class CommandLineTests(Fixture):
    def test_derive_and_verify_commands(self):
        self.assertEqual(rc.main(["derive", "--root", str(self.src), "--out-dir", str(self.out)]), 0)
        self.assertEqual(rc.main(["verify", "--dir", str(self.out)]), 0)
        (self.out / rc.OVERLAY).write_text("services: {}\n", encoding="utf-8")
        self.assertEqual(rc.main(["verify", "--dir", str(self.out)]), 1)


if __name__ == "__main__":
    unittest.main()
