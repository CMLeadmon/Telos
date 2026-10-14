#!/usr/bin/env python3
"""Record a guided smoke test for one platform of the browser or desktop matrix.

    record-guided-smoke.py --matrix browser|desktop --platform ID --candidate-lock PATH --artifact PATH --out PATH
                           [--answers-file FILE] [--signers FILE] [--draft-dir DIR]

The recorder first proves the candidate lock is valid and that --artifact is the exact artifact of that lock (the
hosted-client image for the browser matrix, the AppImage for the desktop matrix). It then shows ONE numbered
question at a time (release/guided-assertions.json) and accepts only pass, fail or not_run plus a bounded note;
credential-like text in a note is replaced by [redacted]. Attachments are hashed and copied beside the record. It
cannot write an overall verdict: the aggregator derives `passed` only when every answer is pass.

The finished record is written as canonical bytes and signed with the key in TELOS_EVIDENCE_SIGNING_KEY for the
principal in TELOS_EVIDENCE_SIGNING_PRINCIPAL (a tester enrolled in release/evidence-signers, namespace
telos-guided-smoke-v1). With no enrolled tester or key the record is saved as an UNSIGNED DRAFT outside the importable
records directory and the command exits 3 (not_run).

--answers-file takes {"applicationVersion","os","osVersion","deviceModel","hostId","assertions":[{"id","answer","notes"}],
"attachments":[paths]} for scripted fixture runs; the answers are validated exactly as typed ones.
Exit codes: 0 signed record written, 1 refused, 2 usage, 3 only a draft could be saved.
"""

import argparse
import hashlib
import json
import os
import platform
import re
import shutil
import sys
from pathlib import Path

sys.dont_write_bytecode = True
HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE / "lib"))
import guided_smoke as gs  # noqa: E402
import release_graph as rg  # noqa: E402
import signing  # noqa: E402

REPO = HERE.parent
CREDENTIAL = re.compile(r"(?i)(password|passwd|secret|token|bearer|authorization|api[_-]?key)\s*[:=]\s*\S+|-----BEGIN [A-Z ]*PRIVATE KEY-----|AKIA[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9]{20,}")
ANSWERS = ("pass", "fail", "not_run")


def file_sha256(path):
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def redact(text):
    return CREDENTIAL.sub("[redacted]", text)[:1000]


def detect_context(matrix):
    """Defaults a tool can know; the tester confirms or corrects them."""
    context = {"os": platform.system() or "", "osVersion": platform.release(), "deviceModel": "", "hostId": ""}
    try:
        for line in Path("/etc/os-release").read_text(encoding="utf-8").splitlines():
            key, _, value = line.partition("=")
            if key == "NAME":
                context["os"] = value.strip('"')
            if key == "VERSION_ID":
                context["osVersion"] = value.strip('"')
    except OSError:
        pass
    if matrix == "desktop":
        try:
            context["hostId"] = "sha256:" + hashlib.sha256(Path("/etc/machine-id").read_bytes()).hexdigest()
        except OSError:
            pass
    return context


def ask(prompt, default="", input_fn=input, output=sys.stdout):
    suffix = f" [{default}]" if default else ""
    output.write(f"{prompt}{suffix}: ")
    output.flush()
    value = input_fn("").strip()
    return value or default


def interactive_answers(catalog, matrix, platform_id, defaults, input_fn, output):
    questions = gs.assertions_for(catalog, matrix, platform_id)
    answers = {}
    output.write("\nDescribe what you are testing. Nothing here is guessed for you except what is detected.\n")
    answers["applicationVersion"] = ask("Browser or application name and exact version", "", input_fn, output)
    answers["os"] = ask("Operating system", defaults["os"], input_fn, output)
    answers["osVersion"] = ask("Operating system version", defaults["osVersion"], input_fn, output)
    answers["deviceModel"] = ask("Device model (the physical device)", defaults["deviceModel"], input_fn, output)
    answers["hostId"] = ask("Stable device id (sha256 of the machine id, or an id you assign)", defaults["hostId"], input_fn, output)
    total = len(questions)
    answers["assertions"] = []
    for number, item in enumerate(questions, 1):
        output.write(f"\n[{number}/{total}] {item['id']}\n  {item['text']}\n")
        while True:
            answer = ask("  pass / fail / not_run", "", input_fn, output).lower()
            if answer in ANSWERS:
                break
            output.write("  Please answer exactly pass, fail or not_run.\n")
        notes = redact(ask("  note (optional, no credentials)", "", input_fn, output))
        answers["assertions"].append({"id": item["id"], "answer": answer, "notes": notes})
    answers["attachments"] = []
    output.write("\nAttach screenshots or logs (full paths); an empty line finishes. Do not attach anything containing credentials.\n")
    while True:
        path = ask("  attachment", "", input_fn, output)
        if not path:
            break
        answers["attachments"].append(path)
    return answers


def build_record(catalog, args, lock, lock_digest, answers, started, finished, out_dir):
    questions = gs.assertions_for(catalog, args.matrix, args.platform)
    given = {a["id"]: a for a in answers.get("assertions", [])}
    if [a["id"] for a in answers.get("assertions", [])] != [a["id"] for a in questions]:
        raise gs.GuidedError("the answers must cover every question, once each, in order")
    assertions = []
    for item in questions:
        entry = given[item["id"]]
        if entry["answer"] not in ANSWERS:
            raise gs.GuidedError(f"{item['id']}: the answer must be pass, fail or not_run")
        assertions.append({"id": item["id"], "answer": entry["answer"], "notes": redact(entry.get("notes", ""))})
    attachments, mapping = gs.attachment_refs(args.platform, answers.get("attachments", []))
    principal = os.environ.get("TELOS_EVIDENCE_SIGNING_PRINCIPAL", "unsigned-tester")
    record = {
        "schemaVersion": 1, "matrix": args.matrix, "platform": args.platform,
        "sourceCommit": lock["sourceCommit"], "candidateLockDigest": lock_digest,
        "artifactDigest": gs.matrix_artifact_digest(catalog, args.matrix, lock),
        "applicationVersion": answers["applicationVersion"], "os": answers["os"], "osVersion": answers["osVersion"],
        "deviceModel": answers["deviceModel"], "hostId": answers["hostId"],
        "startedAt": gs.stamp(started), "finishedAt": gs.stamp(finished), "testerPrincipal": principal,
        "assertions": assertions, "attachments": attachments,
    }
    return record, mapping


def main(argv=None, input_fn=input, output=sys.stdout):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--matrix", required=True, choices=["browser", "desktop"])
    parser.add_argument("--platform", required=True)
    parser.add_argument("--candidate-lock", required=True)
    parser.add_argument("--artifact", required=True)
    parser.add_argument("--out", required=True)
    parser.add_argument("--answers-file")
    parser.add_argument("--signers", default=str(REPO / "release" / "evidence-signers"))
    parser.add_argument("--draft-dir", default=os.path.join(os.path.expanduser("~"), ".cache", "telos", "guided-drafts"))
    args = parser.parse_args(argv)
    try:
        catalog = gs.load_catalog()
        platforms = catalog["matrices"][args.matrix]["platforms"]
        if args.platform not in platforms:
            raise gs.GuidedError(f"{args.platform!r} is not a platform of the {args.matrix} matrix ({', '.join(platforms)})")
        if platforms[args.platform]["mode"] != "guided":
            raise gs.GuidedError(f"{args.platform} is automated; its record comes from the Playwright report tool, not from a person")
        lock_bytes = Path(args.candidate_lock).read_bytes()
        lock = rg.load_record("candidate-lock", lock_bytes, canonical=False)
        lock_digest = rg.digest_bytes(lock_bytes)
        artifact = Path(args.artifact)
        if artifact.is_symlink() or not artifact.is_file():
            raise gs.GuidedError("--artifact must be a regular file")
        if "sha256:" + file_sha256(artifact) != gs.matrix_artifact_digest(catalog, args.matrix, lock):
            raise gs.GuidedError("--artifact is not the exact artifact of this candidate lock for this matrix")
        out = Path(args.out)
        if os.path.lexists(out):
            raise gs.GuidedError("the output record already exists; a record is never overwritten")
        started = gs.now_utc()
        if args.answers_file:
            answers = json.loads(Path(args.answers_file).read_text(encoding="utf-8"))
        else:
            answers = interactive_answers(catalog, args.matrix, args.platform, detect_context(args.matrix), input_fn, output)
        finished = gs.now_utc()
        record, mapping = build_record(catalog, args, lock, lock_digest, answers, started, finished, out.parent)
        # Everything but the signature is checked before a byte is written.
        status = gs.validate_record(record, catalog, lock, lock_digest, now=finished)
        data = rg.canonical_bytes(record)
        code, message = gs.write_record(record, mapping, out, args.signers, args.draft_dir, os.environ.get("TELOS_EVIDENCE_SIGNING_KEY"),
                                        os.environ.get("TELOS_EVIDENCE_SIGNING_PRINCIPAL"), status)
        print(f"record-guided-smoke: {message}", file=sys.stderr if code else sys.stdout)
        return code
    except (gs.GuidedError, rg.GraphError, signing.SigningError, json.JSONDecodeError, KeyError) as error:
        print(f"record-guided-smoke: {error}", file=sys.stderr)
        return 1
    except OSError as error:
        print(f"record-guided-smoke: {error.__class__.__name__}: {str(error)[:200]}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
