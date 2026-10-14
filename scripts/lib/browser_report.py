#!/usr/bin/env python3
"""Turn a Playwright JSON report into the signed automated record of one browser platform.

    browser_report.py --platform chrome|firefox|edge|webkit --playwright-json FILE --candidate-lock FILE --artifact FILE
                      --browser-version TEXT --started ISO --finished ISO --out FILE [--os NAME --os-version V --host-id ID]
                      [--signers FILE] [--draft-dir DIR]

Each journey of the platform's automated list (release/guided-assertions.json) is answered from the tests whose title
carries `@assertion:<id>` and whose project is this platform: every such test passed -> pass; any failed, timed out,
was interrupted or needed a retry -> fail; none ran, or all were skipped -> not_run. A journey with no test is not_run,
never a pass. The raw report is NOT attached (it can carry URLs, headers and cookies); a derived per-test summary is.
The record is signed like a guided one (TELOS_EVIDENCE_SIGNING_KEY / _PRINCIPAL; exit 3 and an unsigned draft without).
"""

import argparse
import json
import os
import platform as host_platform
import re
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
sys.dont_write_bytecode = True
sys.path.insert(0, str(HERE))
import guided_smoke as gs  # noqa: E402
import release_graph as rg  # noqa: E402
import signing  # noqa: E402

TAG = re.compile(r"@assertion:([a-z0-9]+(?:[.-][a-z0-9]+)*)")
BAD_RESULT = {"failed", "timedOut", "interrupted"}


def collect_tests(report):
    """Yield (title, project, status, result statuses) for every test of a Playwright JSON report."""
    def walk(suite, titles):
        for spec in suite.get("specs", []):
            for test in spec.get("tests", []):
                yield (spec.get("title", ""), test.get("projectName"), test.get("status"), [r.get("status") for r in test.get("results", [])])
        for child in suite.get("suites", []):
            yield from walk(child, titles + [child.get("title", "")])
    for suite in report.get("suites", []):
        yield from walk(suite, [suite.get("title", "")])


def answer_journeys(report, platform, questions):
    tests = [t for t in collect_tests(report) if t[1] == platform]
    answers, summary = [], []
    for question in questions:
        mine = [t for t in tests if question["id"] in TAG.findall(t[0])]
        if not mine:
            answers.append({"id": question["id"], "answer": "not_run", "notes": "no test covers this journey"})
            continue
        statuses = [t[2] for t in mine]
        results = [r for t in mine for r in t[3]]
        if any(s in ("unexpected", "flaky") for s in statuses) or any(r in BAD_RESULT for r in results):
            answer = "fail"
        elif all(s == "skipped" for s in statuses) or not any(r == "passed" for r in results):
            answer = "not_run"
        else:
            answer = "pass"
        answers.append({"id": question["id"], "answer": answer, "notes": f"{len(mine)} test(s): " + ", ".join(sorted(set(statuses)))})
        summary += [{"journey": question["id"], "test": re.sub(TAG, "", t[0]).strip()[:120], "status": t[2]} for t in mine]
    return answers, summary


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--platform", required=True)
    parser.add_argument("--playwright-json", required=True)
    parser.add_argument("--candidate-lock", required=True)
    parser.add_argument("--artifact", required=True)
    parser.add_argument("--browser-version", required=True)
    parser.add_argument("--started", required=True)
    parser.add_argument("--finished", required=True)
    parser.add_argument("--out", required=True)
    parser.add_argument("--os", dest="os_name")
    parser.add_argument("--os-version")
    parser.add_argument("--host-id")
    parser.add_argument("--signers", default=str(REPO / "release" / "evidence-signers"))
    parser.add_argument("--draft-dir", default=os.path.join(os.path.expanduser("~"), ".cache", "telos", "guided-drafts"))
    args = parser.parse_args(argv)
    try:
        catalog = gs.load_catalog()
        platforms = catalog["matrices"]["browser"]["platforms"]
        if args.platform not in platforms or platforms[args.platform]["mode"] != "automated":
            raise gs.GuidedError(f"{args.platform!r} is not an automated browser platform ({', '.join(p for p, s in platforms.items() if s['mode'] == 'automated')})")
        lock_bytes = Path(args.candidate_lock).read_bytes()
        lock = rg.load_record("candidate-lock", lock_bytes, canonical=False)
        lock_digest = rg.digest_bytes(lock_bytes)
        artifact = Path(args.artifact)
        if artifact.is_symlink() or not artifact.is_file() or "sha256:" + gs.file_sha256(artifact) != gs.matrix_artifact_digest(catalog, "browser", lock):
            raise gs.GuidedError("--artifact is not the exact hosted-client artifact of this candidate lock")
        out = Path(args.out)
        if os.path.lexists(out):
            raise gs.GuidedError("the output record already exists; a record is never overwritten")
        report = json.loads(Path(args.playwright_json).read_text(encoding="utf-8"))
        questions = gs.assertions_for(catalog, "browser", args.platform)
        answers, summary = answer_journeys(report, args.platform, questions)
        with tempfile.TemporaryDirectory() as scratch:
            summary_path = Path(scratch) / "summary.json"
            summary_path.write_bytes(rg.canonical_bytes({"schemaVersion": 1, "platform": args.platform, "tests": summary}))
            refs, mapping = gs.attachment_refs(args.platform, [summary_path])
            principal = os.environ.get("TELOS_EVIDENCE_SIGNING_PRINCIPAL", "unsigned-runner")
            record = {
                "schemaVersion": 1, "matrix": "browser", "platform": args.platform, "sourceCommit": lock["sourceCommit"], "candidateLockDigest": lock_digest,
                "artifactDigest": gs.matrix_artifact_digest(catalog, "browser", lock), "applicationVersion": args.browser_version,
                "os": args.os_name or host_platform.system(), "osVersion": args.os_version or host_platform.release(), "deviceModel": gs.AUTOMATED_DEVICE,
                "hostId": args.host_id or "automation-host", "startedAt": args.started, "finishedAt": args.finished, "testerPrincipal": principal,
                "assertions": [{"id": a["id"], "answer": a["answer"], "notes": a["notes"]} for a in answers], "attachments": refs,
            }
            status = gs.validate_record(record, catalog, lock, lock_digest, now=gs.now_utc())
            code, message = gs.write_record(record, mapping, out, args.signers, args.draft_dir, os.environ.get("TELOS_EVIDENCE_SIGNING_KEY"),
                                            os.environ.get("TELOS_EVIDENCE_SIGNING_PRINCIPAL"), status)
        print(f"browser-report: {message}", file=sys.stderr if code else sys.stdout)
        return code
    except (gs.GuidedError, rg.GraphError, signing.SigningError, json.JSONDecodeError, KeyError) as error:
        print(f"browser-report: {error}", file=sys.stderr)
        return 1
    except OSError as error:
        print(f"browser-report: {error.__class__.__name__}: {str(error)[:200]}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
