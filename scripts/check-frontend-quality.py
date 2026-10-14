#!/usr/bin/env python3
"""Source checks for the two frontend quality gates that need no running node.

    check-frontend-quality.py accessibility    the accessibility audit machinery is in place and honest
    check-frontend-quality.py quality          the bundle budget and the candidate journey inventory agree

These gates do NOT claim conformance. WCAG evidence is candidate-run: the browser-matrix gate carries the axe-core audit of
every shell route (an automated journey) and the keyboard, zoom and layout questions a person answers on real devices. What
a source gate can prove is that the audit exists, covers every route, cannot be silently dropped, and that no document
claims a certification that has no evidence behind it.
Exit 0 passed, 1 failed (the findings are printed), 2 usage.
"""

import json
import os
import re
import sys
from pathlib import Path

ROOT = Path(os.environ.get("TELOS_CHECK_ROOT") or Path(__file__).resolve().parents[1])
AXE_TAGS = ("wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa")
TAGGED = re.compile(r"@assertion:([a-z0-9]+(?:[.-][a-z0-9]+)*)")


def read(relative):
    path = ROOT / relative
    return path.read_text(encoding="utf-8") if path.is_file() else None


def shell_routes():
    text = read("frontend/src/components/AppShell.tsx") or ""
    hrefs = re.findall(r'\{\s*href:\s*"(/[a-z]+)"', text)
    return sorted(set(hrefs) | {"/settings"})


def catalog():
    text = read("release/guided-assertions.json")
    return json.loads(text) if text else None


def accessibility():
    findings = []
    spec = read("frontend/e2e/hosted-node.spec.ts")
    if spec is None:
        return ["frontend/e2e/hosted-node.spec.ts is missing"]
    if "@assertion:accessibility-axe" not in spec:
        findings.append("hosted-node.spec.ts has no @assertion:accessibility-axe journey")
    def array(name):
        found = re.search(r"const " + name + r"\s*=\s*\[(.*?)\];", spec, re.S)
        return found.group(1) if found else ""

    routes = set(re.findall(r'"(/[a-z]+)/"', array("AXE_ROUTES")))
    for route in shell_routes():
        if route not in routes:
            findings.append(f"the axe journey does not visit {route}/")
    tags = set(re.findall(r'"([a-z0-9]+)"', array("AXE_TAGS")))
    for tag in AXE_TAGS:
        if tag not in tags:
            findings.append(f"the axe journey does not run the {tag} rules")
    widths = set(re.findall(r"width:\s*(\d+)", array("AXE_VIEWPORTS")))
    for width in ("390", "1440"):
        if width not in widths:
            findings.append(f"the axe journey does not run at {width}px")
    if 'impact === "serious" || v.impact === "critical"' not in spec:
        findings.append("the axe journey does not fail on serious and critical violations")
    document = catalog()
    if document is None:
        findings.append("release/guided-assertions.json is missing")
    else:
        browser = document["matrices"]["browser"]
        if "accessibility-axe" not in {a["id"] for a in browser["automatedAssertions"]}:
            findings.append("the automated journey list lacks accessibility-axe")
        guided = next((a for a in browser["assertions"] if a["id"] == "responsive-accessible"), None)
        if guided is None or not all(word in guided["text"].lower() for word in ("keyboard", "focus", "zoom")):
            findings.append("the guided list lacks the keyboard, focus and zoom question")
    lint = read("frontend/eslint.config.mjs") or ""
    if "core-web-vitals" not in lint:
        findings.append("eslint does not extend next/core-web-vitals, which carries the jsx-a11y rules")
    audit = (read("frontend/e2e/accessibility-audit.md") or "").lower()
    if re.search(r"status:\*\*\s*certified|0 known wcag|certified —", audit):
        findings.append("accessibility-audit.md claims a certification; a checklist is not evidence")
    if not (ROOT / "frontend/node_modules/axe-core/axe.min.js").is_file() and (ROOT / "frontend/node_modules").is_dir():
        findings.append("axe-core is not installed")
    lock = read("frontend/package-lock.json") or ""
    if '"node_modules/axe-core"' not in lock:
        findings.append("axe-core is not in package-lock.json")
    return findings


def quality():
    findings = []
    budget_text = read("frontend/bundle-budget.json")
    if budget_text is None:
        return ["frontend/bundle-budget.json is missing"]
    budget = json.loads(budget_text)
    if not isinstance(budget.get("maxGzipBytesPerRoute"), int) or budget["maxGzipBytesPerRoute"] <= 0:
        findings.append("the bundle budget has no positive per-route limit")
    missing = [r for r in shell_routes() if r not in budget.get("routes", [])]
    if missing:
        findings.append(f"the bundle budget omits routes: {missing}")
    config = read("frontend/playwright.config.ts") or ""
    for project in ("chrome", "firefox", "edge", "webkit"):
        if f'name: "{project}"' not in config:
            findings.append(f"playwright.config.ts has no candidate project {project}")
    for flag in ('trace: "off"', 'video: "off"', 'screenshot: "off"', "retries: 0"):
        if flag not in config:
            findings.append(f"the candidate Playwright mode does not set {flag}")
    spec = read("frontend/e2e/hosted-node.spec.ts") or ""
    tagged = set()
    for title in re.findall(r'test\("([^"]+)"', spec):
        tagged.update(TAGGED.findall(title))
    document = catalog()
    coverage_text = read("frontend/e2e/candidate-coverage.json")
    if document is None or coverage_text is None:
        return findings + ["release/guided-assertions.json or frontend/e2e/candidate-coverage.json is missing"]
    coverage = json.loads(coverage_text)
    listed = {a["id"] for a in document["matrices"]["browser"]["automatedAssertions"]}
    uncovered = set(coverage.get("uncovered", {}))
    for journey in sorted(listed):
        if journey not in tagged and journey not in uncovered:
            findings.append(f"journey {journey} has no tagged test and is not listed as uncovered")
        if journey in tagged and journey in uncovered:
            findings.append(f"journey {journey} is both tagged and listed as uncovered")
    for extra in sorted(tagged - listed):
        findings.append(f"a test is tagged with {extra}, which is not an automated journey")
    for stale in sorted(uncovered - listed):
        findings.append(f"candidate-coverage.json lists {stale}, which is not an automated journey")
    for journey, reason in coverage.get("uncovered", {}).items():
        if not isinstance(reason, str) or len(reason) < 20:
            findings.append(f"the uncovered journey {journey} gives no real reason")
    return findings


def main(argv=None):
    argv = sys.argv[1:] if argv is None else argv
    if len(argv) != 1 or argv[0] not in ("accessibility", "quality"):
        print("usage: check-frontend-quality.py accessibility|quality", file=sys.stderr)
        return 2
    try:
        findings = accessibility() if argv[0] == "accessibility" else quality()
    except (ValueError, KeyError, OSError) as error:
        findings = [f"the inputs could not be read: {error.__class__.__name__}: {str(error)[:120]}"]
    for finding in findings:
        print(f"frontend-{argv[0]}: {finding}", file=sys.stderr)
    if not findings:
        print(f"frontend-{argv[0]}: ok")
    return 1 if findings else 0


if __name__ == "__main__":
    sys.exit(main())
