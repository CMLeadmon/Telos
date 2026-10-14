#!/usr/bin/env python3
"""The edge login rate limit must be per client address, not one bucket for everybody.

Traefik's `ipStrategy.depth` reads the client's X-Forwarded-For header; a Traefik that is itself the edge
receives none, so the extracted key is empty for every request and ALL sign-ins share one bucket. The
capacity drill found this on a real node: a 10-per-minute budget for the whole community. The fix is to
leave the criterion at its default (the connection's remote address).
"""

import re
import sys
import unittest
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[2]


class EdgePolicyTests(unittest.TestCase):
    def middleware(self):
        document = yaml.safe_load((ROOT / "config" / "dynamic" / "routes.yaml").read_text(encoding="utf-8"))
        return document["http"]["middlewares"]["telos-login-rate"]["rateLimit"]

    def test_the_login_limit_is_ten_a_minute_with_a_burst_of_five(self):
        limit = self.middleware()
        self.assertEqual((limit["average"], limit["period"], limit["burst"]), (10, "1m", 5))

    def test_the_bucket_is_keyed_by_the_remote_address_never_by_forwarded_for_depth(self):
        limit = self.middleware()
        criterion = limit.get("sourceCriterion") or {}
        strategy = criterion.get("ipStrategy") or {}
        self.assertNotIn("depth", strategy, "depth keys on X-Forwarded-For, which an edge never receives: one global bucket")
        self.assertNotIn("requestHeaderName", criterion)
        self.assertNotIn("requestHost", criterion)

    def test_no_route_in_the_shipped_config_uses_a_depth_strategy(self):
        text = (ROOT / "config" / "dynamic" / "routes.yaml").read_text(encoding="utf-8")
        code = "\n".join(line for line in text.splitlines() if not line.lstrip().startswith("#"))
        self.assertIsNone(re.search(r"^\s*depth\s*:", code, re.M))


if __name__ == "__main__":
    unittest.main(verbosity=1)
