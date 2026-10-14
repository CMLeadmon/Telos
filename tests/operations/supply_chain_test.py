#!/usr/bin/env python3
"""Supply-chain gates: locked tools, SBOM generation and validation, secrets, vulnerabilities, licenses and
risk acceptances -- every refusal exercised on its own against an otherwise valid fixture.

Hermetic: the scanners are stand-in executables that write what the real ones write. This proves the policy and
the result contract; it is NOT a scan of any real artifact.
"""

import copy
import datetime
import hashlib
import importlib.util
import io
import json
import os
import stat
import subprocess
import sys
import tarfile
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts" / "lib"))
import release_graph as rg  # noqa: E402
import supply_chain as sc  # noqa: E402

spec = importlib.util.spec_from_file_location("bootstrap_ci_tools", ROOT / "scripts" / "bootstrap-ci-tools.py")
bootstrap = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bootstrap)

NOW = datetime.datetime(2026, 10, 14, 12, 0, 0, tzinfo=datetime.timezone.utc)
COMMIT = "1" * 40


def sha(data):
    return hashlib.sha256(data).hexdigest()


# ------------------------------------------------------------------ the tool lock and bootstrap

def tar_bytes(members):
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode="w:gz") as tar:
        for name, content in members.items():
            info = tarfile.TarInfo(name)
            info.size = len(content)
            info.mode = 0o755
            tar.addfile(info, io.BytesIO(content))
    return buffer.getvalue()


def lock_for(archive, binary, url="https://github.com/o/tool/releases/download/v1.2.3/tool_1.2.3_linux_amd64.tar.gz", member="tool"):
    return {"schemaVersion": 2, "tools": {"tool": {"version": "1.2.3", "platforms": {"linux-amd64": {
        "url": url, "sha256": sha(archive), "member": member, "binarySha256": sha(binary)}}}}}


class BootstrapTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.cache = Path(self.tmp.name) / "cache"
        self.binary = b"#!/bin/sh\necho tool 1.2.3\n"
        self.archive = tar_bytes({"README": b"x", "tool": self.binary})

    def lock(self, **kwargs):
        return bootstrap.load_lock(self.write(lock_for(self.archive, self.binary, **kwargs)))

    def write(self, document):
        path = Path(self.tmp.name) / "tools.lock"
        path.write_text(json.dumps(document))
        return str(path)

    def test_a_correct_download_is_verified_extracted_and_cached(self):
        calls = []
        lock = self.lock()
        path = bootstrap.bootstrap(lock, "tool", self.cache, downloader=lambda url: calls.append(url) or self.archive)
        self.assertEqual(Path(path).read_bytes(), self.binary)
        self.assertEqual(stat.S_IMODE(os.stat(path).st_mode), 0o755)
        self.assertEqual(bootstrap.bootstrap(lock, "tool", self.cache, downloader=lambda url: self.fail("cached binary re-downloaded")), path)
        self.assertEqual(len(calls), 1)

    def test_a_download_that_does_not_match_the_lock_is_never_extracted(self):
        lock = self.lock()
        with self.assertRaisesRegex(bootstrap.Refused, "does not match the locked SHA-256; nothing was extracted"):
            bootstrap.bootstrap(lock, "tool", self.cache, downloader=lambda url: tar_bytes({"tool": b"evil"}))
        self.assertFalse((self.cache / "tool").exists())

    def test_a_tampered_cached_binary_is_replaced_not_run(self):
        lock = self.lock()
        path = bootstrap.bootstrap(lock, "tool", self.cache, downloader=lambda url: self.archive)
        Path(path).write_bytes(b"tampered")
        again = bootstrap.bootstrap(lock, "tool", self.cache, downloader=lambda url: self.archive)
        self.assertEqual(Path(again).read_bytes(), self.binary)

    def test_a_wrong_binary_digest_unlisted_tool_and_unlisted_platform_are_refused(self):
        document = lock_for(self.archive, self.binary)
        document["tools"]["tool"]["platforms"]["linux-amd64"]["binarySha256"] = "0" * 64
        with self.assertRaisesRegex(bootstrap.Refused, "extracted binary does not match"):
            bootstrap.bootstrap(bootstrap.load_lock(self.write(document)), "tool", self.cache, downloader=lambda url: self.archive)
        lock = self.lock()
        with self.assertRaisesRegex(bootstrap.Refused, "not a locked tool"):
            bootstrap.bootstrap(lock, "other", self.cache)
        with self.assertRaisesRegex(bootstrap.Refused, "no locked build for darwin-arm64"):
            bootstrap.bootstrap(lock, "tool", self.cache, platform="darwin-arm64")

    def test_an_archive_cannot_choose_what_is_extracted(self):
        for members in ({"../tool": self.binary}, {"x/tool": self.binary}, {"README": b"x"}):
            archive = tar_bytes(members)
            document = lock_for(archive, self.binary)
            with self.assertRaisesRegex(bootstrap.Refused, "exactly one regular file"):
                bootstrap.bootstrap(bootstrap.load_lock(self.write(document)), "tool", self.cache, downloader=lambda url, a=archive: a)

    def test_a_malformed_lock_is_refused(self):
        def refused(mutate):
            document = lock_for(self.archive, self.binary)
            mutate(document)
            with self.assertRaises(bootstrap.Refused):
                bootstrap.load_lock(self.write(document))
        platform = lambda d: d["tools"]["tool"]["platforms"]["linux-amd64"]  # noqa: E731
        refused(lambda d: platform(d).pop("sha256"))
        refused(lambda d: platform(d).update(sha256="ABC"))
        refused(lambda d: platform(d).update(url="http://github.com/o/tool/releases/download/v1.2.3/t.tar.gz"))
        refused(lambda d: platform(d).update(url="https://evil.example/o/tool/v1.2.3/t.tar.gz"))
        refused(lambda d: platform(d).update(url="https://user@github.com/o/tool/v1.2.3/t.tar.gz"))
        refused(lambda d: platform(d).update(url="https://github.com/o/tool/releases/download/v9.9.9/t.tar.gz"))
        refused(lambda d: platform(d).update(member="../tool"))
        refused(lambda d: d.update(schemaVersion=1))
        refused(lambda d: d["tools"]["tool"].update(version="latest"))

    def test_the_cache_must_be_private_and_outside_the_repository(self):
        with self.assertRaises(bootstrap.Refused):
            bootstrap.check_cache("relative/cache")
        with self.assertRaises(bootstrap.Refused):
            bootstrap.check_cache(str(ROOT / ".tool-cache"))
        open_dir = Path(self.tmp.name) / "open"
        open_dir.mkdir(mode=0o755)
        with self.assertRaises(bootstrap.Refused):
            bootstrap.check_cache(str(open_dir))

    def test_a_system_tool_is_accepted_only_when_bytes_and_version_match(self):
        lock = self.lock()
        good = Path(self.tmp.name) / "tool"
        good.write_bytes(self.binary)
        good.chmod(0o755)
        self.assertEqual(bootstrap.verify_system_tool(lock, "tool", str(good)), str(good))
        good.write_bytes(b"#!/bin/sh\necho tool 9.9.9\n")
        with self.assertRaisesRegex(bootstrap.Refused, "SHA-256 differs"):
            bootstrap.verify_system_tool(lock, "tool", str(good))
        with self.assertRaises(bootstrap.Refused):
            bootstrap.verify_system_tool(lock, "tool", "tool")   # a bare name would be a PATH lookup

    def test_the_shipped_lock_is_valid_and_names_the_three_scanners(self):
        lock = bootstrap.load_lock(ROOT / "ci" / "tools.lock")
        self.assertEqual(set(lock["tools"]), {"gitleaks", "syft", "trivy"})


# ------------------------------------------------------------------ risk acceptances

def acceptance(**changes):
    item = {"id": "CVE-2026-1111", "package": "libexpat", "version": "2.6.0", "artifactSha256": "a" * 64, "owner": "release-owner",
            "rationale": "Not reachable: the parser is only fed content from our own build.", "issueUrl": "https://example.org/issues/1",
            "createdAt": "2026-10-10T00:00:00Z", "expiresAt": "2026-10-30T00:00:00Z"}
    item.update(changes)
    return item


class AcceptanceTests(unittest.TestCase):
    def check(self, *items):
        return sc.validate_acceptances({"schemaVersion": 1, "acceptances": list(items)}, NOW)

    def test_an_empty_list_and_a_complete_entry_are_valid(self):
        self.assertEqual(self.check(), [])
        self.assertEqual(len(self.check(acceptance())), 1)

    def test_each_defect_is_refused(self):
        for label, change in (("wildcard package", {"package": "lib*"}), ("pattern id", {"id": "CVE-2026-*"}), ("bad id", {"id": "whatever"}),
                              ("no owner", {"owner": " "}), ("short rationale", {"rationale": "fine"}), ("bad artifact", {"artifactSha256": "abc"}),
                              ("http issue", {"issueUrl": "http://example.org/1"}), ("expired", {"createdAt": "2026-09-01T00:00:00Z", "expiresAt": "2026-10-01T00:00:00Z"}),
                              ("over 30 days", {"createdAt": "2026-10-10T00:00:00Z", "expiresAt": "2026-11-20T00:00:00Z"}),
                              ("expires before creation", {"createdAt": "2026-10-12T00:00:00Z", "expiresAt": "2026-10-11T00:00:00Z"})):
            with self.subTest(label), self.assertRaises(sc.Refused):
                self.check(acceptance(**change))
        item = acceptance()
        item.pop("owner")
        with self.assertRaises(sc.Refused):
            self.check(item)
        with self.assertRaises(sc.Refused):
            self.check(acceptance(), acceptance())
        with self.assertRaises(sc.Refused):
            sc.validate_acceptances({"schemaVersion": 1}, NOW)
        with self.assertRaises(sc.Refused):
            sc.validate_acceptances({"schemaVersion": 1, "acceptances": [], "extra": 1}, NOW)


# ------------------------------------------------------------------ vulnerabilities, secrets, licenses

def trivy(*vulnerabilities):
    return {"Results": [{"Target": "x", "Vulnerabilities": [dict(VulnerabilityID=v[0], PkgName=v[1], InstalledVersion=v[2], Severity=v[3]) for v in vulnerabilities]}]}


class VulnerabilityTests(unittest.TestCase):
    ARTIFACT = "a" * 64

    def evaluate(self, result, acceptances=()):
        return sc.evaluate_vulnerabilities(result, self.ARTIFACT, list(acceptances), NOW)

    def test_a_critical_always_fails_even_with_an_acceptance(self):
        findings, accepted = self.evaluate(trivy(("CVE-2026-1111", "libexpat", "2.6.0", "CRITICAL")), [acceptance()])
        self.assertEqual((len(findings), accepted), (1, []))

    def test_an_unaccepted_high_fails_and_an_exact_acceptance_passes(self):
        high = trivy(("CVE-2026-1111", "libexpat", "2.6.0", "HIGH"))
        self.assertEqual(len(self.evaluate(high)[0]), 1)
        findings, accepted = self.evaluate(high, [acceptance()])
        self.assertEqual((findings, [a["id"] for a in accepted]), ([], ["CVE-2026-1111"]))

    def test_an_acceptance_must_match_advisory_package_version_and_artifact_exactly(self):
        high = trivy(("CVE-2026-1111", "libexpat", "2.6.0", "HIGH"))
        for change in ({"id": "CVE-2026-2222"}, {"package": "zlib"}, {"version": "2.6.1"}, {"artifactSha256": "b" * 64}):
            with self.subTest(change):
                self.assertEqual(len(self.evaluate(high, [acceptance(**change)])[0]), 1)

    def test_an_expired_acceptance_does_not_cover_a_high(self):
        high = trivy(("CVE-2026-1111", "libexpat", "2.6.0", "HIGH"))
        expired = acceptance(createdAt="2026-09-01T00:00:00Z", expiresAt="2026-10-01T00:00:00Z")
        self.assertEqual(len(self.evaluate(high, [expired])[0]), 1)

    def test_lower_severities_do_not_block(self):
        self.assertEqual(self.evaluate(trivy(("CVE-2026-1", "a", "1", "MEDIUM"), ("CVE-2026-2", "b", "1", "LOW")))[0], [])

    def test_secret_findings_fail(self):
        self.assertEqual(sc.evaluate_secrets([]), [])
        self.assertEqual(len(sc.evaluate_secrets([{"RuleID": "github-pat", "File": "a.txt", "StartLine": 3}])), 1)
        self.assertEqual(len(sc.evaluate_secrets({"not": "a list"})), 1)


def spdx_package(name, version, purl_type, license_text, spdx_id=None):
    package = {"SPDXID": spdx_id or f"SPDXRef-{name}-{version}".replace("/", "-").replace("@", "-"), "name": name, "versionInfo": version,
               "externalRefs": [{"referenceCategory": "PACKAGE-MANAGER", "referenceType": "purl", "referenceLocator": f"pkg:{purl_type}/{name}@{version}"}]}
    if license_text is not None:
        package["licenseConcluded"] = license_text
    return package


class LicenseTests(unittest.TestCase):
    POLICY = json.loads((ROOT / "ci" / "license-policy.json").read_text())

    def judge(self, *packages, isolated=None):
        return sc.evaluate_licenses({"packages": list(packages)}, self.POLICY, isolated_name=isolated)

    def test_the_shipped_policy_is_valid(self):
        sc.validate_license_policy(self.POLICY)

    def test_allowed_linked_licenses_pass_and_are_inventoried(self):
        findings, inventory = self.judge(spdx_package("github.com/jackc/pgx", "v5", "golang", "MIT"), spdx_package("react", "19", "npm", "MIT"))
        self.assertEqual((findings, inventory), ([], {"MIT": 2}))

    def test_a_forbidden_missing_or_unlisted_linked_license_fails(self):
        findings, _ = self.judge(spdx_package("evil/gpl", "1", "golang", "GPL-3.0-only"))
        self.assertTrue(findings and "forbidden" not in findings[0] or "GPL" in findings[0])
        self.assertEqual(len(self.judge(spdx_package("x", "1", "golang", "AGPL-3.0"))[0]), 1)
        self.assertEqual(len(self.judge(spdx_package("x", "1", "npm", None))[0]), 1, "no license is a finding for linked code")
        self.assertEqual(len(self.judge(spdx_package("x", "1", "cargo", "LicenseRef-proprietary"))[0]), 1)
        self.assertEqual(len(self.judge(spdx_package("x", "1", "npm", "Beerware"))[0]), 1)

    def test_spdx_expressions_are_evaluated(self):
        allowed = {"MIT", "Apache-2.0"}
        for text, expected in (("MIT OR GPL-3.0-only", True), ("GPL-3.0-only OR AGPL-3.0", False), ("MIT AND Apache-2.0", True),
                               ("MIT AND GPL-3.0-only", False), ("(MIT OR GPL-3.0-only) AND Apache-2.0", True), ("Apache-2.0 WITH LLVM-exception", True),
                               ("GPL-2.0-only WITH Classpath-exception-2.0", False), ("MIT+", True)):
            with self.subTest(text):
                self.assertEqual(sc.expression_allowed(text, allowed), expected)
        with self.assertRaises(sc.Refused):
            sc.expression_allowed("MIT AND (Apache-2.0", allowed)

    def test_an_isolated_service_is_inventoried_not_judged(self):
        findings, inventory = self.judge(spdx_package("jellyfin-server", "10.11", "golang", "GPL-2.0-only"), spdx_package("coreutils", "9", "deb", "GPL-3.0-or-later"), isolated="jellyfin")
        self.assertEqual(findings, [])
        self.assertIn("GPL-2.0-only", inventory)

    def test_base_layer_packages_of_our_own_images_are_inventoried_not_judged(self):
        findings, inventory = self.judge(spdx_package("busybox", "1.37", "apk", "GPL-2.0-only"), spdx_package("github.com/x/y", "1", "golang", "MIT"))
        self.assertEqual(findings, [])
        self.assertEqual(inventory, {"GPL-2.0-only": 1, "MIT": 1})

    def test_isolated_service_code_in_a_linked_graph_is_flagged(self):
        sboms = {"core": {"packages": [spdx_package("github.com/jellyfin/jellyfin-sdk", "1", "golang", "MIT"), spdx_package("grimmory", "3", "npm", "MIT")]}}
        findings = sc.isolated_code_in_linked(sboms, list(self.POLICY["isolatedServices"]), self.POLICY)
        self.assertEqual(len(findings), 2)
        clean = {"core": {"packages": [spdx_package("github.com/jackc/pgx", "5", "golang", "MIT")]}}
        self.assertEqual(sc.isolated_code_in_linked(clean, list(self.POLICY["isolatedServices"]), self.POLICY), [])

    def test_a_policy_that_allows_what_it_forbids_is_refused(self):
        bad = copy.deepcopy(self.POLICY)
        bad["forbiddenLinked"].append("MIT")
        with self.assertRaises(sc.Refused):
            sc.validate_license_policy(bad)
        bad = copy.deepcopy(self.POLICY)
        bad["linkedArtifacts"]["go-core"]["allowed"] = []
        with self.assertRaises(sc.Refused):
            sc.validate_license_policy(bad)


# ------------------------------------------------------------------ SBOMs

SYFT_VERSION = json.loads((ROOT / "ci" / "tools.lock").read_text())["tools"]["syft"]["version"]
SYFT_DIGEST = json.loads((ROOT / "ci" / "tools.lock").read_text())["tools"]["syft"]["platforms"]["linux-amd64"]["binarySha256"]


def spdx(artifact_sha, packages=None, creators=None, created="2026-10-14T11:00:00Z"):
    return {"spdxVersion": "SPDX-2.3", "dataLicense": "CC0-1.0", "SPDXID": "SPDXRef-DOCUMENT", "name": f"telos-artifact-{artifact_sha}",
            "creationInfo": {"created": created, "creators": creators or [f"Tool: syft-{SYFT_VERSION}"]},
            "packages": packages if packages is not None else [spdx_package("github.com/jackc/pgx", "v5.9.2", "golang", "MIT")]}


class SbomValidationTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.dir = Path(self.tmp.name)
        self.sboms = self.dir / "sboms"
        self.sboms.mkdir()
        self.artifacts = {"images/core.tar": "a" * 64, "images/db.tar": "b" * 64, "generation/g.tar": "c" * 64}
        self.lock_path = self.dir / "candidate-lock.json"
        self.index = {"schemaVersion": 1, "tool": {"name": "syft", "version": SYFT_VERSION, "binarySha256": SYFT_DIGEST}, "dependencyLocks": {}, "entries": []}
        for path, digest in self.artifacts.items():
            if path.startswith("generation"):
                self.index["entries"].append({"artifactPath": path, "artifactSha256": digest, "kind": "generation", "sbom": None, "exempt": "not a package set", "packages": 0})
                continue
            document = spdx(digest)
            data = rg.canonical_bytes(document)
            (self.sboms / f"{digest}.spdx.json").write_bytes(data)
            self.index["entries"].append({"artifactPath": path, "artifactSha256": digest, "kind": "oci-image",
                                          "sbom": {"path": f"{digest}.spdx.json", "sha256": sha(data)}, "exempt": None, "packages": 1})
        self.write_lock()
        self.write_index()

    def write_lock(self, artifacts=None):
        lock = {"schemaVersion": 1, "version": "0.1.0-beta.1", "sourceCommit": COMMIT, "artifactIndexDigest": "sha256:" + "d" * 64,
                "releaseManifestDigest": "sha256:" + "e" * 64, "artifacts": {p: "sha256:" + d for p, d in (artifacts or self.artifacts).items()}}
        self.lock_path.write_bytes(rg.canonical_bytes(lock))

    def write_index(self):
        (self.sboms / sc.SBOM_INDEX).write_bytes(rg.canonical_bytes(self.index))

    def validate(self, **kwargs):
        return sc.validate_sbom_dir(self.lock_path, self.sboms, now=NOW, **kwargs)

    def refused(self, fragment):
        with self.assertRaises(sc.Failed) as raised:
            self.validate()
        self.assertTrue(any(fragment in finding for finding in raised.exception.findings), raised.exception.findings)

    def entry(self, path):
        return next(e for e in self.index["entries"] if e["artifactPath"] == path)

    def test_a_complete_bound_set_validates(self):
        index, documents = self.validate()
        self.assertEqual(len(documents), 2)

    def test_a_missing_artifact_entry_or_file_fails(self):
        self.index["entries"] = [e for e in self.index["entries"] if e["artifactPath"] != "images/db.tar"]
        self.write_index()
        self.refused("no SBOM index entry")
        self.setUp()
        (self.sboms / f"{'a' * 64}.spdx.json").unlink()
        self.refused("is missing")

    def test_a_lock_that_includes_the_sbom_files_themselves_still_validates(self):
        artifacts = dict(self.artifacts)
        artifacts["sbom/sbom-index.json"] = sha((self.sboms / sc.SBOM_INDEX).read_bytes())
        artifacts["sbom/" + "a" * 64 + ".spdx.json"] = sha((self.sboms / f"{'a' * 64}.spdx.json").read_bytes())
        self.write_lock(artifacts)
        self.validate()

    def test_a_missing_sbom_is_not_excused_by_the_sbom_directory_exemption(self):
        artifacts = dict(self.artifacts)
        artifacts["images/other.tar"] = "f" * 64   # an image outside sbom/ with no index entry still fails
        self.write_lock(artifacts)
        with self.assertRaises(sc.Failed):
            self.validate()

    def test_an_oci_image_without_an_sbom_fails(self):
        self.entry("images/core.tar").update(sbom=None, exempt="trust me")
        self.write_index()
        self.refused("must have an SBOM")

    def test_an_sbom_bound_to_another_artifact_or_with_changed_bytes_fails(self):
        data = rg.canonical_bytes(spdx("f" * 64))
        (self.sboms / f"{'a' * 64}.spdx.json").write_bytes(data)
        self.entry("images/core.tar")["sbom"]["sha256"] = sha(data)
        self.write_index()
        self.refused("not bound to this artifact")
        self.setUp()
        (self.sboms / f"{'a' * 64}.spdx.json").write_bytes(b"{}")
        self.refused("bytes differ from the index")

    def test_wrong_creator_duplicate_packages_and_empty_sboms_fail(self):
        def replace(digest, document):
            data = rg.canonical_bytes(document)
            (self.sboms / f"{digest}.spdx.json").write_bytes(data)
            self.entry_for(digest)["sbom"]["sha256"] = sha(data)
            self.write_index()
        self.entry_for = lambda digest: next(e for e in self.index["entries"] if e["artifactSha256"] == digest)
        replace("a" * 64, spdx("a" * 64, creators=["Tool: syft-0.1.0"]))
        self.refused("not created by the locked syft")
        self.setUp()
        self.entry_for = lambda digest: next(e for e in self.index["entries"] if e["artifactSha256"] == digest)
        package = spdx_package("github.com/x/y", "1", "golang", "MIT")
        replace("a" * 64, spdx("a" * 64, packages=[package, dict(package, SPDXID="SPDXRef-other")]))
        self.refused("listed twice")
        self.setUp()
        self.entry_for = lambda digest: next(e for e in self.index["entries"] if e["artifactSha256"] == digest)
        replace("a" * 64, spdx("a" * 64, packages=[]))
        self.refused("lists no packages")

    def test_an_sbom_for_an_artifact_the_candidate_does_not_contain_fails(self):
        self.write_lock({"images/core.tar": "a" * 64, "generation/g.tar": "c" * 64})
        self.refused("which the candidate does not contain")

    def test_an_index_from_another_tool_fails(self):
        self.index["tool"]["version"] = "0.0.1"
        self.write_index()
        self.refused("locked syft")
        self.setUp()
        self.index["tool"]["binarySha256"] = "0" * 64
        self.write_index()
        self.refused("not the locked one")

    def test_a_stale_sbom_fails_when_an_age_limit_is_given(self):
        with self.assertRaises(sc.Failed):
            self.validate(max_age_hours=0)

    def test_the_spdx_checker_alone(self):
        self.assertEqual(sc.validate_spdx(spdx("a" * 64), "telos-artifact-" + "a" * 64, SYFT_VERSION), [])
        broken = spdx("a" * 64)
        broken["dataLicense"] = "MIT"
        broken["SPDXID"] = "x"
        broken["creationInfo"]["created"] = "yesterday"
        self.assertEqual(len(sc.validate_spdx(broken, "telos-artifact-" + "a" * 64, SYFT_VERSION)), 3)


# ------------------------------------------------------------------ generation and scanning with stand-in tools

STUB_SYFT = '''#!/usr/bin/env python3
import json, sys
args = sys.argv[1:]
name = args[args.index("--source-name") + 1]
target = args[args.index("-o") + 1].split("=", 1)[1]
packages = json.load(open(%(scenario)r))["packages"]
json.dump({"spdxVersion": "SPDX-2.3", "dataLicense": "CC0-1.0", "SPDXID": "SPDXRef-DOCUMENT", "name": name,
           "creationInfo": {"created": "2026-10-14T11:00:00Z", "creators": ["Tool: syft-%(version)s"]}, "packages": packages}, open(target, "w"))
'''
STUB_TRIVY = '''#!/usr/bin/env python3
import json, os, sys
args = sys.argv[1:]
scenario = json.load(open(%(scenario)r))
if "--download-db-only" in args:
    cache = args[args.index("--cache-dir") + 1]
    os.makedirs(os.path.join(cache, "db"), exist_ok=True)
    open(os.path.join(cache, "db", "trivy.db"), "wb").write(b"database")
    json.dump({"DownloadedAt": scenario["dbDownloadedAt"], "UpdatedAt": scenario["dbDownloadedAt"]}, open(os.path.join(cache, "db", "metadata.json"), "w"))
    sys.exit(0)
output = args[args.index("--output") + 1]
json.dump(scenario["trivy"], open(output, "w"))
'''
STUB_GITLEAKS = '''#!/usr/bin/env python3
import json, sys
args = sys.argv[1:]
json.dump(json.load(open(%(scenario)r))["gitleaks"], open(args[args.index("--report-path") + 1], "w"))
open(%(scenario)r + ".gitleaks-argv", "w").write(json.dumps(args))
'''


class PipelineTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.dir = Path(self.tmp.name)
        self.tools = self.dir / "tools"
        self.tools.mkdir(mode=0o700)
        self.artifact_root = self.dir / "artifacts"
        (self.artifact_root / "images").mkdir(parents=True)
        self.core = b"core image archive"
        self.db = b"database image archive"
        (self.artifact_root / "images" / "telos-core.tar").write_bytes(self.core)
        (self.artifact_root / "images" / "jellyfin.tar").write_bytes(self.db)
        self.scenario_path = self.dir / "scenario.json"
        self.scenario = {"packages": [spdx_package("github.com/jackc/pgx", "v5.9.2", "golang", "MIT"), spdx_package("busybox", "1.37", "apk", "GPL-2.0-only")],
                         "trivy": {"Results": []}, "gitleaks": [], "dbDownloadedAt": "2026-10-14T10:00:00Z"}
        self.save()
        self.stubs = {}
        for name, template in (("syft", STUB_SYFT), ("trivy", STUB_TRIVY), ("gitleaks", STUB_GITLEAKS)):
            path = self.tools / name
            path.write_text("#!/usr/bin/env python3\n" + (template % {"scenario": str(self.scenario_path), "version": SYFT_VERSION}).split("\n", 1)[1])
            path.chmod(0o755)
            self.stubs[name] = str(path)
        self.payload = {"schemaVersion": 1, "artifacts": [   # the artifact index is sorted by path
            {"contentDigest": "sha256:" + "7" * 64, "kind": "oci-image", "path": "images/jellyfin.tar", "platform": "linux/amd64", "sha256": sha(self.db), "size": len(self.db)},
            {"contentDigest": "sha256:" + "8" * 64, "kind": "oci-image", "path": "images/telos-core.tar", "platform": "linux/amd64", "sha256": sha(self.core), "size": len(self.core)}]}
        self.payload_path = self.dir / "payload-index.json"
        self.payload_path.write_bytes(rg.canonical_bytes(self.payload))
        self.sbom_dir = self.dir / "sboms"

    def save(self):
        self.scenario_path.write_text(json.dumps(self.scenario))

    def binary(self, tools_dir, name):
        versions = json.loads((ROOT / "ci" / "tools.lock").read_text())["tools"]
        return self.stubs[name], versions[name]["version"], versions[name]["platforms"]["linux-amd64"]["binarySha256"]

    def generate(self):
        return sc.generate_sbom(self.payload_path, self.artifact_root, self.sbom_dir, str(self.tools), binary=self.binary)

    def write_lock(self):
        artifacts = {a["path"]: "sha256:" + a["sha256"] for a in self.payload["artifacts"]}
        lock = {"schemaVersion": 1, "version": "0.1.0-beta.1", "sourceCommit": COMMIT, "artifactIndexDigest": "sha256:" + "d" * 64,
                "releaseManifestDigest": "sha256:" + "e" * 64, "artifacts": artifacts}
        self.lock_path = self.dir / "candidate-lock.json"
        self.lock_path.write_bytes(rg.canonical_bytes(lock))
        return self.lock_path

    def scan(self, **kwargs):
        lock = self.write_lock()
        source = self.dir / "src"
        source.mkdir(exist_ok=True)
        return sc.scan(lock, self.artifact_root, self.sbom_dir, str(self.tools), source_dir=str(source), binary=self.binary, now=NOW,
                       acceptances_path=kwargs.pop("acceptances_path", sc.RISK_ACCEPTANCES), **kwargs)

    def test_generation_writes_one_bound_sbom_per_image_and_an_index(self):
        index = self.generate()
        self.assertEqual(sorted(e["artifactPath"] for e in index["entries"] if e["sbom"]), ["images/jellyfin.tar", "images/telos-core.tar"])
        entry = next(e for e in index["entries"] if e["artifactPath"] == "images/telos-core.tar")
        document = json.loads((self.sbom_dir / entry["sbom"]["path"]).read_text())
        self.assertEqual(document["name"], f"telos-artifact-{sha(self.core)}")
        self.assertNotIn("sha256:", json.dumps(document["name"]), "an SBOM never carries a candidate digest")
        self.write_lock()
        sc.validate_sbom_dir(self.lock_path, self.sbom_dir, now=NOW)

    def test_generation_refuses_artifacts_that_no_longer_match_the_index(self):
        (self.artifact_root / "images" / "telos-core.tar").write_bytes(b"swapped after indexing")
        with self.assertRaisesRegex(sc.Refused, "no longer matches the payload index"):
            self.generate()

    def test_other_artifact_kinds_are_exempt_with_a_reason_and_syft_failures_are_refused(self):
        self.payload["artifacts"].insert(0, {"contentDigest": None, "kind": "generation", "path": "generation/g.tar", "platform": "any", "sha256": sha(b"g"), "size": 1})
        (self.artifact_root / "generation").mkdir()
        (self.artifact_root / "generation" / "g.tar").write_bytes(b"g")
        self.payload_path.write_bytes(rg.canonical_bytes(self.payload))
        index = self.generate()
        exempt = next(e for e in index["entries"] if e["kind"] == "generation")
        self.assertTrue(exempt["exempt"] and exempt["sbom"] is None)
        (self.tools / "syft").write_text("#!/bin/sh\nexit 3\n")
        with self.assertRaisesRegex(sc.Refused, "syft failed"):
            self.generate()

    def test_a_clean_scan_passes_and_records_tools_database_and_lock(self):
        self.generate()
        report, findings = self.scan()
        self.assertEqual(findings, [])
        self.assertEqual(report["status"], "passed")
        self.assertEqual(report["candidateLockDigest"], rg.digest_bytes(self.lock_path.read_bytes()))
        self.assertEqual(report["vulnerabilityDatabase"]["retrievedAt"], "2026-10-14T10:00:00Z")
        self.assertTrue(report["vulnerabilityDatabase"]["digest"].startswith("sha256:"))
        self.assertEqual(set(report["tools"]), {"trivy", "gitleaks", "syft"})
        self.assertEqual(report["secretFindings"], 0)

    def test_the_source_trees_own_gitleaks_allowlist_is_named_explicitly_and_recorded(self):
        self.generate()
        source = self.dir / "src"
        source.mkdir(exist_ok=True)
        argv_file = Path(str(self.scenario_path) + ".gitleaks-argv")
        report, findings = self.scan()
        self.assertNotIn("--config", json.loads(argv_file.read_text()), "no allowlist in the tree: the default ruleset applies in full")
        self.assertIsNone(report["gitleaksConfigSha256"])
        (source / ".gitleaks.toml").write_text('title = "x"\n')
        report, findings = self.scan()
        argv = json.loads(argv_file.read_text())
        self.assertEqual(argv[argv.index("--config") + 1], str(source / ".gitleaks.toml"))
        self.assertEqual(report["gitleaksConfigSha256"], rg.raw_sha256(b'title = "x"\n'))

    def test_a_critical_an_unaccepted_high_and_a_secret_each_fail(self):
        self.generate()
        self.scenario["trivy"] = trivy(("CVE-2026-9", "libx", "1", "CRITICAL"))
        self.save()
        self.assertTrue(any("CRITICAL" in f for f in self.scan()[1]))
        self.scenario["trivy"] = trivy(("CVE-2026-9", "libx", "1", "HIGH"))
        self.save()
        self.assertTrue(any("HIGH" in f for f in self.scan()[1]))
        self.scenario["trivy"] = {"Results": []}
        self.scenario["gitleaks"] = [{"RuleID": "github-pat", "File": "x", "StartLine": 1}]
        self.save()
        self.assertTrue(any("secret" in f for f in self.scan()[1]))

    def test_an_exact_acceptance_lets_a_high_through_and_is_reported(self):
        self.generate()
        self.scenario["trivy"] = trivy(("CVE-2026-1111", "libexpat", "2.6.0", "HIGH"))
        self.save()
        path = self.dir / "acceptances.json"
        both = [acceptance(artifactSha256=sha(self.core)), acceptance(artifactSha256=sha(self.db))]
        path.write_text(json.dumps({"schemaVersion": 1, "acceptances": both}))
        report, findings = self.scan(acceptances_path=path)
        self.assertEqual(findings, [])
        self.assertEqual(sorted(a["artifact"] for a in report["acceptancesUsed"]), ["images/jellyfin.tar", "images/telos-core.tar"])
        # an acceptance covers exactly one artifact: with only the core image accepted, the other image's High still blocks
        path.write_text(json.dumps({"schemaVersion": 1, "acceptances": both[:1]}))
        findings = self.scan(acceptances_path=path)[1]
        self.assertEqual(len(findings), 1)
        self.assertIn("images/jellyfin.tar", findings[0])

    def test_an_old_vulnerability_database_fails(self):
        self.generate()
        self.scenario["dbDownloadedAt"] = "2026-10-12T00:00:00Z"
        self.save()
        self.assertTrue(any("hours old" in f for f in self.scan()[1]))

    def test_a_forbidden_linked_license_and_isolated_code_in_a_linked_graph_fail(self):
        self.scenario["packages"] = [spdx_package("evil/gpl", "1", "golang", "GPL-3.0-only")]
        self.save()
        self.generate()
        self.assertTrue(any("GPL-3.0" in f for f in self.scan()[1]))
        self.scenario["packages"] = [spdx_package("github.com/jellyfin/sdk", "1", "golang", "MIT")]
        self.save()
        self.generate()
        self.assertTrue(any("isolated third-party service" in f for f in self.scan()[1]))

    def test_an_isolated_gpl_service_is_inventoried_not_failed(self):
        self.scenario["packages"] = [spdx_package("jellyfin-server", "10.11", "deb", "GPL-2.0-only"), spdx_package("github.com/x/y", "1", "golang", "GPL-2.0-only")]
        self.save()
        self.generate()
        report, findings = self.scan()
        # telos-core.tar carries the golang GPL package (a finding); jellyfin.tar is isolated and only inventoried
        self.assertTrue(all("jellyfin.tar" not in f or "isolated" in f for f in findings))
        self.assertEqual(report["licenses"]["images/jellyfin.tar"]["class"], "isolated-service")

    def test_a_scan_that_cannot_run_is_refused_not_passed(self):
        self.generate()
        (self.tools / "trivy").write_text("#!/bin/sh\nexit 4\n")
        with self.assertRaisesRegex(sc.Refused, "database could not be refreshed"):
            self.scan()

    def test_the_gate_result_is_closed_bound_and_carries_the_artifact_subject_only_on_a_pass(self):
        self.generate()
        report, findings = self.scan()
        out = self.dir / "out"
        out.mkdir()
        lock_bytes = self.lock_path.read_bytes()
        status = sc.write_scan_result(out / "result.json", "rehearsal", COMMIT, rg.digest_bytes(lock_bytes), report, findings, lock_bytes)
        self.assertEqual(status, "passed")
        result = rg.verify_result_directory(out, "result.json")
        self.assertEqual((result["status"], result["gateId"]), ("passed", "candidate-supply-chain"))
        subject = rg.load_record("observed-subject", (out / "subject.json").read_bytes())
        self.assertEqual((subject["kind"], subject["selector"]), ("artifact", "artifact-index"))
        out2 = self.dir / "out2"
        out2.mkdir()
        status = sc.write_scan_result(out2 / "result.json", "rehearsal", COMMIT, rg.digest_bytes(lock_bytes), report, ["CRITICAL x"], lock_bytes)
        self.assertEqual(status, "failed")
        self.assertFalse((out2 / "subject.json").exists())


    def command(self, *extra, rehearsal=True, commit=COMMIT):
        """`supply_chain.py scan ...` through main(), with the scanners stubbed and the clock fixed."""
        from unittest import mock
        lock = self.write_lock()
        source = self.dir / "src"
        source.mkdir(exist_ok=True)
        out = self.dir / f"out-{len(list(self.dir.glob('out-*')))}"
        out.mkdir()
        real = sc.scan
        argv = ["scan", "--artifact-root", str(self.artifact_root), "--sbom-dir", str(self.sbom_dir), "--result-out", str(out / "result.json"),
                "--tools-dir", str(self.tools), "--source-dir", str(source)]
        argv += (["--rehearsal-lock", str(lock)] + (["--source-commit", commit] if commit else [])) if rehearsal else ["--candidate-lock", str(lock)] + (["--source-commit", commit] if commit else [])
        with mock.patch.object(sc, "scan", side_effect=lambda *a, **k: real(*a, binary=self.binary, now=NOW)):
            code = sc.main(argv + list(extra))
        return code, out

    def test_the_scan_command_writes_the_labelled_record_and_maps_the_verdict_to_the_exit_code(self):
        self.generate()
        code, out = self.command()
        self.assertEqual(code, 0)
        result = rg.verify_result_directory(out, "result.json")
        self.assertEqual((result["recordKind"], result["status"], result["gateId"]), ("rehearsal", "passed", "candidate-supply-chain"))
        self.assertTrue((out / "subject.json").exists() and (out / "scan-report.json").exists())
        code, out = self.command(rehearsal=False, commit=None)
        self.assertEqual(code, 0)
        self.assertEqual(rg.verify_result_directory(out, "result.json")["recordKind"], "candidate")
        self.scenario["trivy"] = {"Results": [{"Target": "x", "Vulnerabilities": [
            {"VulnerabilityID": "CVE-2026-9", "PkgName": "libx", "InstalledVersion": "1", "Severity": "CRITICAL"}]}]}
        self.save()
        code, out = self.command()
        self.assertEqual(code, sc.EXIT_FAILED)
        failed = rg.verify_result_directory(out, "result.json")
        self.assertEqual(failed["status"], "failed")
        self.assertFalse((out / "subject.json").exists(), "a failing scan has no artifact subject")

    def test_the_scan_command_refuses_a_commit_that_does_not_belong_to_the_lock_kind(self):
        self.generate()
        self.assertEqual(self.command(commit="2" * 40)[0], sc.EXIT_USAGE, "the rehearsal commit must be the one the lock was built from")
        code, out = self.command(commit=None)   # the lock carries the commit; naming it is optional, contradicting it is not
        self.assertEqual(code, 0)
        self.assertEqual(rg.verify_result_directory(out, "result.json")["sourceCommit"], COMMIT)
        self.assertEqual(self.command(rehearsal=False)[0], sc.EXIT_USAGE, "a candidate scan takes its commit from the lock, never from the command line")


class OciLayoutTests(unittest.TestCase):
    """Trivy takes an OCI image only as a layout directory, so OCI-archive tarballs are unpacked first (found by the live scan)."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.dir = Path(self.tmp.name)

    def tar(self, name, members):
        import io, tarfile
        path = self.dir / name
        with tarfile.open(path, "w") as bundle:
            for member, data in members:
                if isinstance(member, tarfile.TarInfo):
                    bundle.addfile(member)
                    continue
                info = tarfile.TarInfo(member)
                info.size = len(data)
                bundle.addfile(info, io.BytesIO(data))
        return path

    def test_an_oci_archive_is_unpacked_to_a_layout_directory(self):
        archive = self.tar("a.oci.tar", [("oci-layout", b'{"imageLayoutVersion":"1.0.0"}'), ("index.json", b"{}"), ("blobs/sha256/abc", b"layer")])
        layout = sc.extract_oci_layout(archive, self.dir)
        self.assertEqual(sorted(p.name for p in layout.iterdir()), ["blobs", "index.json", "oci-layout"])
        self.assertEqual((layout / "blobs" / "sha256" / "abc").read_bytes(), b"layer")

    def test_a_docker_archive_and_a_non_tar_are_scanned_as_they_are(self):
        self.assertIsNone(sc.extract_oci_layout(self.tar("d.tar", [("manifest.json", b"[]")]), self.dir))
        (self.dir / "x.tar").write_bytes(b"not a tar at all")
        self.assertIsNone(sc.extract_oci_layout(self.dir / "x.tar", self.dir))

    def test_an_unsafe_member_is_refused_and_nothing_is_written_outside(self):
        import tarfile
        link = tarfile.TarInfo("blobs/evil")
        link.type, link.linkname = tarfile.SYMTYPE, "/etc/passwd"
        for name, members in (("traversal.tar", [("oci-layout", b"{}"), ("index.json", b"{}"), ("../escaped", b"x")]),
                              ("absolute.tar", [("oci-layout", b"{}"), ("index.json", b"{}"), ("/tmp/telos-escaped", b"x")]),
                              ("link.tar", [("oci-layout", b"{}"), ("index.json", b"{}"), (link, b"")])):
            with self.subTest(name):
                work = self.dir / ("work-" + name)
                work.mkdir()
                with self.assertRaises(sc.Refused):
                    sc.extract_oci_layout(self.tar(name, members), work)
                self.assertFalse((self.dir / "escaped").exists())
                self.assertEqual([p.name for p in (work / "oci-layout").rglob("*")], [], "nothing is extracted before every member passed")


class ModuleShapeTests(unittest.TestCase):
    def test_no_top_level_definition_appears_twice(self):
        # A splice once left a second copy of write_scan_result, check_policy and a stale main behind; the later copy silently won at
        # import and the live scan crashed (found by the first live run).
        import ast
        tree = ast.parse((ROOT / "scripts/lib/supply_chain.py").read_text(encoding="utf-8"))
        names = [n.name for n in tree.body if isinstance(n, (ast.FunctionDef, ast.ClassDef))]
        self.assertEqual(sorted({n for n in names if names.count(n) > 1}), [])


class CommandLineTests(unittest.TestCase):
    def test_policy_command_passes_on_the_repository(self):
        completed = subprocess.run([sys.executable, str(ROOT / "scripts/lib/supply_chain.py"), "policy"], capture_output=True, text=True)
        self.assertEqual(completed.returncode, 0, completed.stderr)

    def test_wrappers_refuse_without_inputs(self):
        for script in ("scan-candidate.sh", "ci/validate-sbom.sh"):
            completed = subprocess.run(["bash", str(ROOT / "scripts" / script)], capture_output=True, text=True)
            self.assertEqual(completed.returncode, 3, f"{script}: {completed.stderr}")
        self.assertEqual(subprocess.run(["bash", str(ROOT / "scripts/generate-sbom.sh")], capture_output=True).returncode, 2)


if __name__ == "__main__":
    unittest.main(verbosity=1)
