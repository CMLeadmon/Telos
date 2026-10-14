#!/usr/bin/env python3
"""The frontend source quality checks: they pass on the repository and refuse each way the audit could be hollowed out."""

import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
CHECKER = ROOT / "scripts" / "check-frontend-quality.py"
FILES = [
    "frontend/e2e/hosted-node.spec.ts", "frontend/e2e/candidate-coverage.json", "frontend/e2e/accessibility-audit.md",
    "frontend/src/components/AppShell.tsx", "frontend/bundle-budget.json", "frontend/playwright.config.ts",
    "frontend/eslint.config.mjs", "frontend/package-lock.json", "release/guided-assertions.json",
]


class Fixture(unittest.TestCase):
    def setUp(self):
        self.dir = Path(tempfile.mkdtemp(prefix="frontend-quality-"))
        self.addCleanup(shutil.rmtree, self.dir, True)
        for relative in FILES:
            target = self.dir / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(ROOT / relative, target)

    def check(self, which):
        done = subprocess.run([sys.executable, str(CHECKER), which], capture_output=True, text=True, env=dict(os.environ, TELOS_CHECK_ROOT=str(self.dir)))
        return done.returncode, done.stderr + done.stdout

    def edit(self, relative, old, new):
        path = self.dir / relative
        text = path.read_text(encoding="utf-8")
        self.assertIn(old, text)
        path.write_text(text.replace(old, new, 1), encoding="utf-8")

    def refuses(self, which, fragment):
        code, output = self.check(which)
        self.assertEqual(code, 1, output)
        self.assertIn(fragment, output)


class RepositoryTests(unittest.TestCase):
    def test_the_repository_passes_both_checks(self):
        for which in ("accessibility", "quality"):
            done = subprocess.run([sys.executable, str(CHECKER), which], capture_output=True, text=True)
            self.assertEqual(done.returncode, 0, done.stderr)

    def test_usage_errors(self):
        self.assertEqual(subprocess.run([sys.executable, str(CHECKER)], capture_output=True).returncode, 2)
        self.assertEqual(subprocess.run([sys.executable, str(CHECKER), "everything"], capture_output=True).returncode, 2)


class AccessibilityTests(Fixture):
    def test_a_fixture_copy_passes(self):
        self.assertEqual(self.check("accessibility")[0], 0)

    def test_dropping_the_axe_journey(self):
        self.edit("frontend/e2e/hosted-node.spec.ts", "@assertion:accessibility-axe", "@assertion:chat-axe")
        self.refuses("accessibility", "no @assertion:accessibility-axe journey")

    def test_dropping_a_route_a_ruleset_or_a_viewport(self):
        self.edit("frontend/e2e/hosted-node.spec.ts", 'const AXE_ROUTES = ["/chat/", "/stream/", "/library/", "/files/", "/settings/"];',
                  'const AXE_ROUTES = ["/chat/", "/stream/", "/library/", "/settings/"];')
        self.refuses("accessibility", "does not visit /files/")

    def test_dropping_a_wcag_ruleset(self):
        self.edit("frontend/e2e/hosted-node.spec.ts", '"wcag22aa"]', "]")
        self.refuses("accessibility", "wcag22aa")

    def test_dropping_a_viewport(self):
        self.edit("frontend/e2e/hosted-node.spec.ts", "{ width: 390, height: 844 },\n  { width: 1440, height: 900 },", "{ width: 1440, height: 900 },")
        self.refuses("accessibility", "390px")

    def test_an_audit_that_no_longer_fails_on_serious_violations(self):
        self.edit("frontend/e2e/hosted-node.spec.ts", 'v.impact === "serious" || v.impact === "critical"', 'v.impact === "critical"')
        self.refuses("accessibility", "serious and critical")

    def test_a_catalog_without_the_journey_or_the_guided_question(self):
        path = self.dir / "release/guided-assertions.json"
        document = json.loads(path.read_text())
        document["matrices"]["browser"]["automatedAssertions"] = [a for a in document["matrices"]["browser"]["automatedAssertions"] if a["id"] != "accessibility-axe"]
        path.write_text(json.dumps(document))
        self.refuses("accessibility", "lacks accessibility-axe")
        document = json.loads((ROOT / "release/guided-assertions.json").read_text())
        for entry in document["matrices"]["browser"]["assertions"]:
            if entry["id"] == "responsive-accessible":
                entry["text"] = "It looks fine."
        path.write_text(json.dumps(document))
        self.refuses("accessibility", "keyboard, focus and zoom")

    def test_a_certification_claim_without_evidence(self):
        (self.dir / "frontend/e2e/accessibility-audit.md").write_text("**Status:** Certified — 0 Known WCAG 2.2 AA Failures\n")
        self.refuses("accessibility", "claims a certification")

    def test_losing_the_lint_rules_or_the_dependency(self):
        self.edit("frontend/eslint.config.mjs", "core-web-vitals", "recommended")
        self.refuses("accessibility", "jsx-a11y")
        self.edit("frontend/package-lock.json", '"node_modules/axe-core"', '"node_modules/axe-core-gone"')
        self.refuses("accessibility", "axe-core is not in package-lock.json")


class QualityTests(Fixture):
    def test_a_fixture_copy_passes(self):
        self.assertEqual(self.check("quality")[0], 0)

    def test_a_route_missing_from_the_bundle_budget(self):
        self.edit("frontend/bundle-budget.json", '"/files",', "")
        self.refuses("quality", "omits routes")

    def test_a_candidate_project_removed(self):
        self.edit("frontend/playwright.config.ts", '{ name: "edge",', '{ name: "edgy",')
        self.refuses("quality", "no candidate project edge")

    def test_credential_bearing_artifacts_must_stay_off(self):
        self.edit("frontend/playwright.config.ts", 'trace: "off" as const', 'trace: "on" as const')
        self.refuses("quality", 'trace: "off"')

    def test_a_journey_with_neither_a_test_nor_a_reason(self):
        path = self.dir / "frontend/e2e/candidate-coverage.json"
        document = json.loads(path.read_text())
        del document["uncovered"]["offline-reconnect"]
        path.write_text(json.dumps(document))
        self.refuses("quality", "journey offline-reconnect has no tagged test and is not listed as uncovered")

    def test_a_tagged_journey_still_listed_as_uncovered(self):
        path = self.dir / "frontend/e2e/candidate-coverage.json"
        document = json.loads(path.read_text())
        document["uncovered"]["chat"] = "this journey has a test but is still listed here"
        path.write_text(json.dumps(document))
        self.refuses("quality", "both tagged and listed as uncovered")

    def test_a_tag_for_a_journey_that_does_not_exist(self):
        self.edit("frontend/e2e/hosted-node.spec.ts", "@assertion:logout-login", "@assertion:logout-login @assertion:imaginary")
        self.refuses("quality", "imaginary")

    def test_an_uncovered_entry_without_a_real_reason(self):
        path = self.dir / "frontend/e2e/candidate-coverage.json"
        document = json.loads(path.read_text())
        document["uncovered"]["responsive-layout"] = "later"
        path.write_text(json.dumps(document))
        self.refuses("quality", "gives no real reason")


if __name__ == "__main__":
    unittest.main()
