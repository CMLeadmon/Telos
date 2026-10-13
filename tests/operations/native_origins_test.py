#!/usr/bin/env python3
"""The beta's cross-origin surface is exactly the desktop shell's two origins."""

import os
import sys
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
sys.path.insert(0, os.path.join(ROOT, "cli", "lib"))
import node_config  # noqa: E402

PAIR = "tauri://localhost,http://tauri.localhost"


class AllowedOriginsTests(unittest.TestCase):
    def test_production_accepts_exactly_the_pair_in_either_order(self):
        self.assertEqual(node_config.validate_allowed_origins(PAIR, True), [])
        self.assertEqual(node_config.validate_allowed_origins("http://tauri.localhost,tauri://localhost", True), [])

    def test_production_requires_both(self):
        for raw in ("", "tauri://localhost", "http://tauri.localhost"):
            self.assertTrue(node_config.validate_allowed_origins(raw, True), raw)

    def test_hostile_or_unsupported_entries_are_defects_everywhere(self):
        hostile = [
            "*", "null", "https://evil.example", "http://localhost", "http://localhost:3000", "tauri://localhost/",
            "tauri://localhost/path", "tauri://localhost?x=1", "tauri://user@localhost", "tauri://localhost.evil.example",
            "TAURI://LOCALHOST", "http://tauri.localhost:80", "https://tauri.localhost",
        ]
        for entry in hostile:
            for production in (True, False):
                raw = f"{PAIR},{entry}"
                self.assertTrue(node_config.validate_allowed_origins(raw, production), (entry, production))
                self.assertTrue(node_config.validate_allowed_origins(entry, production), (entry, production))

    def test_duplicates_and_padding_are_rejected(self):
        self.assertTrue(node_config.validate_allowed_origins("tauri://localhost,tauri://localhost,http://tauri.localhost", True))
        self.assertTrue(node_config.validate_allowed_origins("tauri://localhost, http://tauri.localhost", True))
        self.assertTrue(node_config.validate_allowed_origins("tauri://localhost,,http://tauri.localhost", True))

    def test_development_may_leave_it_empty_or_use_the_pair(self):
        self.assertEqual(node_config.validate_allowed_origins("", False), [])
        self.assertEqual(node_config.validate_allowed_origins(PAIR, False), [])
        self.assertEqual(node_config.validate_allowed_origins("tauri://localhost", False), [])

    def test_the_shipped_template_carries_the_supported_pair(self):
        text = open(os.path.join(ROOT, ".env.example"), encoding="utf-8").read()
        self.assertIn("\nTELOS_ALLOWED_ORIGINS=" + PAIR + "\n", text)

    def test_compose_passes_the_value_to_the_gateway(self):
        text = open(os.path.join(ROOT, "docker-compose.yml"), encoding="utf-8").read()
        self.assertIn("TELOS_ALLOWED_ORIGINS=${TELOS_ALLOWED_ORIGINS:-}", text)


if __name__ == "__main__":
    unittest.main(verbosity=1)
