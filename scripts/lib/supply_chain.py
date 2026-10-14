#!/usr/bin/env python3
"""Supply-chain policy for immutable release artifacts: SBOMs, vulnerabilities, secrets, licenses.

    supply_chain.py policy                                       validate ci/tools.lock, ci/license-policy.json, ci/risk-acceptances.json
    supply_chain.py generate-sbom --payload-index F --artifact-root D --out-dir D --tools-dir D [--root REPO]
    supply_chain.py validate-sbom --candidate-lock F --sbom-dir D [--max-age-hours N]
    supply_chain.py scan --candidate-lock F --artifact-root D --sbom-dir D --result-out F --tools-dir D [--source-dir D]

Policy, in one place:
  * Every tool is a locked release archive (ci/tools.lock) verified before it runs; nothing comes from $PATH.
  * One SPDX JSON SBOM per immutable OCI/desktop artifact, named for the artifact's digest and bound to it by
    name; an index maps every content artifact digest to its SBOM digest (or to the reason none is needed). SBOMs
    never contain the candidate-lock digest (the lock is made from them); the post-freeze scan binds the graph.
  * A secret anywhere in the exact source tree fails. A Critical vulnerability fails. A High fails unless a risk
    acceptance names the exact CVE, package, version and artifact, has an owner, rationale and issue URL, lasts at
    most 30 days and has not expired. An empty acceptance list is valid.
  * Linked code (Go core, browser JS, Rust desktop) may use only the allowed SPDX licenses; a missing license fails.
    Isolated third-party services (Jellyfin, Grimmory, databases ...) are inventoried and credited, not judged as
    linked -- but none of their code may appear in a linked dependency graph.
  * The scanner database must be at most 24 hours old when a scan starts; its digest and retrieval time are recorded.

Exit codes: 0 passed, 1 failed (the findings are on stderr), 2 unsafe or malformed input, 3 not_run.
"""

import argparse
import datetime
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import release_graph as rg  # noqa: E402

ROOT = Path(__file__).resolve().parent.parent.parent
TOOLS_LOCK = ROOT / "ci" / "tools.lock"
LICENSE_POLICY = ROOT / "ci" / "license-policy.json"
RISK_ACCEPTANCES = ROOT / "ci" / "risk-acceptances.json"
SBOM_INDEX = "sbom-index.json"
SBOM_PAYLOAD_DIR = "sbom"
SBOM_KINDS = ("oci-image", "desktop")
LOCK_FILES = ("backend/go.sum", "frontend/package-lock.json", "frontend/src-tauri/Cargo.lock")
MAX_ACCEPTANCE_DAYS = 30
MAX_DB_AGE_SECONDS = 24 * 3600
HEX64 = re.compile(r"[0-9a-f]{64}\Z")
CVE = re.compile(r"(?:CVE-\d{4}-\d{4,}|GHSA-[0-9a-z]{4}-[0-9a-z]{4}-[0-9a-z]{4}|GO-\d{4}-\d{4,})\Z")
TIME = re.compile(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z\Z")
EXIT_FAILED, EXIT_USAGE, EXIT_NOT_RUN = 1, 2, 3


class Refused(Exception):
    """Unsafe or malformed input; the message is safe to print."""


class Failed(Exception):
    """A policy finding; ``findings`` lists them."""

    def __init__(self, findings):
        super().__init__("; ".join(findings))
        self.findings = list(findings)


def parse_time(text):
    if not isinstance(text, str) or not TIME.match(text):
        raise Refused(f"{text!r} is not a UTC timestamp like 2026-10-14T05:00:00Z")
    return datetime.datetime.strptime(text, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=datetime.timezone.utc)


def now_utc():
    return datetime.datetime.now(datetime.timezone.utc)


def stamp(moment):
    return moment.strftime("%Y-%m-%dT%H:%M:%SZ")


def load_json(path, what):
    try:
        return json.loads(Path(path).read_text(encoding="utf-8"))
    except (OSError, ValueError) as error:
        raise Refused(f"cannot read {what} ({error.__class__.__name__})")


# ------------------------------------------------------------------ risk acceptances

ACCEPTANCE_FIELDS = {"id", "package", "version", "artifactSha256", "owner", "rationale", "issueUrl", "createdAt", "expiresAt"}


def validate_acceptances(document, now=None):
    """Return the list of acceptances; raise Refused for any malformed, overbroad or expired entry."""
    now = now or now_utc()
    if not isinstance(document, dict) or document.get("schemaVersion") != 1 or set(document) != {"schemaVersion", "acceptances"} or not isinstance(document["acceptances"], list):
        raise Refused("risk-acceptances.json must be {schemaVersion: 1, acceptances: [...]}")
    seen = set()
    for index, item in enumerate(document["acceptances"]):
        label = f"acceptance {index + 1}"
        if not isinstance(item, dict) or set(item) != ACCEPTANCE_FIELDS:
            raise Refused(f"{label}: it must have exactly {sorted(ACCEPTANCE_FIELDS)}")
        if not CVE.match(str(item["id"])):
            raise Refused(f"{label}: id must be one exact advisory id, not a pattern")
        for key in ("package", "version", "owner", "rationale"):
            if not isinstance(item[key], str) or not item[key].strip() or "*" in item[key]:
                raise Refused(f"{label}: {key} must be a non-empty exact value (no wildcards)")
        if len(item["rationale"]) < 30:
            raise Refused(f"{label}: the rationale must say why the risk is acceptable")
        if not HEX64.match(str(item["artifactSha256"])):
            raise Refused(f"{label}: artifactSha256 must name one artifact")
        if not re.fullmatch(r"https://[A-Za-z0-9._~:/?#\[\]@!$&'()*+,;=%-]+", str(item["issueUrl"])):
            raise Refused(f"{label}: issueUrl must be an https URL")
        created, expires = parse_time(item["createdAt"]), parse_time(item["expiresAt"])
        if expires <= created:
            raise Refused(f"{label}: it expires before it was created")
        if expires - created > datetime.timedelta(days=MAX_ACCEPTANCE_DAYS):
            raise Refused(f"{label}: an acceptance lasts at most {MAX_ACCEPTANCE_DAYS} days")
        if expires <= now:
            raise Refused(f"{label}: expired on {item['expiresAt']}; remove it or fix the finding")
        key = (item["id"], item["package"], item["version"], item["artifactSha256"])
        if key in seen:
            raise Refused(f"{label}: duplicates an earlier acceptance")
        seen.add(key)
    return document["acceptances"]


# ------------------------------------------------------------------ licenses

def validate_license_policy(policy):
    if not isinstance(policy, dict) or policy.get("schemaVersion") != 2 or set(policy) != {"schemaVersion", "linkedArtifacts", "isolatedServices", "forbiddenLinked"}:
        raise Refused("license-policy.json must be schemaVersion 2 with linkedArtifacts, isolatedServices and forbiddenLinked")
    linked = policy["linkedArtifacts"]
    if not isinstance(linked, dict) or not linked:
        raise Refused("linkedArtifacts must name at least the Go core, the browser JavaScript and the Rust desktop bundle")
    for name, entry in linked.items():
        if not isinstance(entry, dict) or set(entry) != {"ecosystem", "allowed"} or not entry["allowed"] or not all(isinstance(l, str) and l for l in entry["allowed"]):
            raise Refused(f"linked artifact {name!r}: ecosystem and a non-empty allowed list are required")
    if not isinstance(policy["isolatedServices"], dict) or not policy["isolatedServices"]:
        raise Refused("isolatedServices must name the separately-run third-party images")
    for name, entry in policy["isolatedServices"].items():
        if not isinstance(entry, dict) or set(entry) != {"why"} or not entry["why"]:
            raise Refused(f"isolated service {name!r} needs a why")
    forbidden = set(policy["forbiddenLinked"])
    allowed = {l for entry in linked.values() for l in entry["allowed"]}
    if forbidden & allowed:
        raise Refused(f"licenses both allowed and forbidden: {sorted(forbidden & allowed)}")
    return policy


def split_expression(expression):
    """Evaluate-friendly tokens of an SPDX license expression (no exceptions: WITH binds to its license)."""
    return re.findall(r"\(|\)|AND|OR|WITH|[A-Za-z0-9.+\-:]+", expression)


def expression_allowed(expression, allowed):
    """Is an SPDX expression satisfiable using only allowed licenses? OR needs one side, AND needs both."""
    tokens = split_expression(expression)
    position = [0]

    def peek():
        return tokens[position[0]] if position[0] < len(tokens) else None

    def take():
        position[0] += 1
        return tokens[position[0] - 1]

    def atom():
        token = take()
        if token == "(":
            value = or_expression()
            if peek() != ")":
                raise Refused(f"unbalanced SPDX expression {expression!r}")
            take()
            return value
        license_id = token
        if peek() == "WITH":
            take()
            take()   # the exception id does not change which license applies
        return license_id in allowed or license_id.rstrip("+") in allowed

    def and_expression():
        value = atom()
        while peek() == "AND":
            take()
            value = atom() and value
        return value

    def or_expression():
        value = and_expression()
        while peek() == "OR":
            take()
            value = and_expression() or value
        return value

    result = or_expression()
    if position[0] != len(tokens):
        raise Refused(f"unparsable SPDX expression {expression!r}")
    return result


def package_license(package):
    for key in ("licenseConcluded", "licenseDeclared"):
        value = package.get(key)
        if value and value not in ("NOASSERTION", "NONE"):
            return value
    return None


def package_ecosystem(package):
    """The purl type (golang, npm, cargo, apk, deb ...) of an SPDX package, or None."""
    for reference in package.get("externalRefs") or []:
        if reference.get("referenceType") == "purl":
            match = re.match(r"pkg:([a-z0-9.+-]+)/", str(reference.get("referenceLocator")))
            if match:
                return match.group(1)
    return None


def evaluate_licenses(sbom, policy, isolated_name=None):
    """(findings, inventory) for one artifact's SBOM.

    An isolated third-party service is inventoried and credited, never judged: it runs as its own program. For
    anything else only LINKED dependencies are judged -- Go modules, npm packages and Cargo crates, the code that
    ends up inside a Telos binary or bundle. The operating-system packages of an image's base layer are separate
    programs shipped beside Telos, so they are inventoried too. A linked package with no license, a forbidden
    one or one outside the allowed list is a finding.
    """
    findings, inventory = [], {}
    linked_by_ecosystem = {entry["ecosystem"]: (name, entry) for name, entry in policy["linkedArtifacts"].items()}
    forbidden = set(policy["forbiddenLinked"])
    for package in sbom.get("packages") or []:
        license_text = package_license(package)
        inventory[license_text or "NOASSERTION"] = inventory.get(license_text or "NOASSERTION", 0) + 1
        if isolated_name is not None:
            continue
        ecosystem = package_ecosystem(package)
        if ecosystem not in linked_by_ecosystem:
            continue
        _, entry = linked_by_ecosystem[ecosystem]
        label = f"{package.get('name')}@{package.get('versionInfo')} ({ecosystem})"
        if license_text is None:
            findings.append(f"{label}: no license is declared")
        elif not expression_allowed(license_text, set(entry["allowed"])):
            tokens = set(split_expression(license_text))
            findings.append(f"{label}: " + (f"forbidden license {license_text}" if tokens & forbidden else f"license {license_text} is not allowed for linked code"))
    return findings, dict(sorted(inventory.items()))


def isolated_code_in_linked(sboms, isolated_names, policy):
    """None of an isolated service's code may be a dependency of linked code."""
    findings = []
    ecosystems = {entry["ecosystem"] for entry in policy["linkedArtifacts"].values()}
    lowered = {n.lower() for n in isolated_names}
    for name, sbom in sboms.items():
        for package in sbom.get("packages") or []:
            if package_ecosystem(package) not in ecosystems:
                continue
            package_name = str(package.get("name") or "").lower()
            if package_name in lowered or any(part in lowered for part in re.split(r"[/@]", package_name) if part):
                findings.append(f"{name}: links {package.get('name')}, which is an isolated third-party service")
    return findings


# ------------------------------------------------------------------ SPDX

def validate_spdx(document, expected_name, locked_syft_version=None):
    findings = []
    if not isinstance(document, dict):
        return ["the SBOM is not a JSON object"]
    if not str(document.get("spdxVersion", "")).startswith("SPDX-2."):
        findings.append("spdxVersion is not SPDX-2.x")
    if document.get("dataLicense") != "CC0-1.0":
        findings.append("dataLicense must be CC0-1.0")
    if document.get("SPDXID") != "SPDXRef-DOCUMENT":
        findings.append("the document SPDXID must be SPDXRef-DOCUMENT")
    if document.get("name") != expected_name:
        findings.append(f"the SBOM is named {document.get('name')!r}, not {expected_name!r}; it is not bound to this artifact")
    info = document.get("creationInfo") or {}
    try:
        parse_time(info.get("created"))
    except Refused:
        findings.append("creationInfo.created is missing or not a UTC timestamp")
    creators = info.get("creators") or []
    if locked_syft_version and f"Tool: syft-{locked_syft_version}" not in creators:
        findings.append(f"the SBOM was not created by the locked syft {locked_syft_version} (creators: {creators[:3]})")
    packages = document.get("packages")
    if not isinstance(packages, list) or not packages:
        findings.append("the SBOM lists no packages")
        return findings
    ids, identities = set(), set()
    for package in packages:
        spdx_id = package.get("SPDXID")
        if not spdx_id or spdx_id in ids:
            findings.append(f"duplicate or missing SPDXID {spdx_id!r}")
        ids.add(spdx_id)
        purls = tuple(sorted(r.get("referenceLocator", "") for r in package.get("externalRefs") or [] if r.get("referenceType") == "purl"))
        identity = (package.get("name"), package.get("versionInfo"), purls, package.get("sourceInfo"))
        if identity in identities:
            findings.append(f"package {package.get('name')}@{package.get('versionInfo')} is listed twice")
        identities.add(identity)
    return findings


# ------------------------------------------------------------------ vulnerabilities and secrets

def evaluate_vulnerabilities(trivy_result, artifact_sha256, acceptances, now=None):
    """(findings, accepted) for one artifact's Trivy JSON. Critical always fails; High needs an exact acceptance."""
    now = now or now_utc()
    findings, accepted = [], []
    index = {(a["id"], a["package"], a["version"], a["artifactSha256"]): a for a in acceptances}
    for result in trivy_result.get("Results") or []:
        for vulnerability in result.get("Vulnerabilities") or []:
            severity = str(vulnerability.get("Severity", "")).upper()
            if severity not in ("CRITICAL", "HIGH"):
                continue
            key = (vulnerability.get("VulnerabilityID"), vulnerability.get("PkgName"), vulnerability.get("InstalledVersion"), artifact_sha256)
            label = f"{key[0]} in {key[1]} {key[2]}"
            if severity == "CRITICAL":
                findings.append(f"CRITICAL {label}")
            elif key in index and parse_time(index[key]["expiresAt"]) > now:
                accepted.append({"id": key[0], "package": key[1], "version": key[2], "owner": index[key]["owner"], "expiresAt": index[key]["expiresAt"]})
            else:
                findings.append(f"HIGH {label} (no unexpired acceptance names exactly this advisory, package, version and artifact)")
    return findings, accepted


def evaluate_secrets(gitleaks_report):
    if not isinstance(gitleaks_report, list):
        return ["the secret scan report is not a list"]
    return [f"secret finding {f.get('RuleID')} in {f.get('File')}:{f.get('StartLine')}" for f in gitleaks_report]


# ------------------------------------------------------------------ tools

def locked_tool_versions(lock):
    return {name: tool["version"] for name, tool in lock["tools"].items()}


def run_tool(argv, timeout=3600, cwd=None):
    completed = subprocess.run(argv, capture_output=True, text=True, timeout=timeout, check=False, cwd=cwd,
                               env={"PATH": "/usr/bin:/bin", "HOME": os.environ.get("HOME", "/tmp"), "LC_ALL": "C"})
    return completed


def verified_binary(tools_dir, name, lock_path=TOOLS_LOCK):
    import importlib.util
    spec = importlib.util.spec_from_file_location("bootstrap_ci_tools", ROOT / "scripts" / "bootstrap-ci-tools.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    lock = module.load_lock(lock_path)
    try:
        path = module.bootstrap(lock, name, tools_dir)
    except module.Refused as error:
        raise Refused(str(error))
    return path, lock["tools"][name]["version"], lock["tools"][name]["platforms"]["linux-amd64"]["binarySha256"]


# ------------------------------------------------------------------ SBOM generation

def read_index(path):
    document = load_json(path, "the payload index")
    return rg.validate_record("artifact-index", document)


def generate_sbom(payload_index, artifact_root, out_dir, tools_dir, repo_root=ROOT, run=run_tool, binary=verified_binary):
    """One SPDX JSON SBOM per OCI/desktop artifact, after re-verifying the artifacts against the index."""
    index = read_index(payload_index)
    root = Path(artifact_root)
    out = Path(out_dir)
    out.mkdir(mode=0o750, parents=True, exist_ok=True)
    syft, version, binary_digest = binary(tools_dir, "syft")
    entries = []
    for artifact in sorted(index["artifacts"], key=lambda a: a["path"]):
        size, digest, _ = rg.file_identity(root, artifact["path"])
        if digest != artifact["sha256"] or size != artifact["size"]:
            raise Refused(f"{artifact['path']} no longer matches the payload index; refusing to describe different bytes")
        entry = {"artifactPath": artifact["path"], "artifactSha256": artifact["sha256"], "kind": artifact["kind"], "sbom": None, "exempt": None, "packages": 0}
        if artifact["kind"] not in SBOM_KINDS:
            entry["exempt"] = f"a {artifact['kind']} is not scanned as a package set; its dependency locks are recorded in the index"
            entries.append(entry)
            continue
        name = f"telos-artifact-{artifact['sha256']}"
        target = out / f"{artifact['sha256']}.spdx.json"
        source = f"oci-archive:{root / artifact['path']}" if artifact["kind"] == "oci-image" else f"file:{root / artifact['path']}"
        completed = run([syft, source, "--source-name", name, "-o", f"spdx-json={target}", "-q"])
        if completed.returncode != 0 or not target.is_file():
            raise Refused(f"syft failed for {artifact['path']}: {completed.stderr.strip()[-200:]}")
        data = target.read_bytes()
        document = json.loads(data)
        problems = validate_spdx(document, name, version)
        if problems:
            raise Refused(f"the SBOM for {artifact['path']} is invalid: {problems[0]}")
        os.chmod(target, 0o640)
        entry["sbom"] = {"path": target.name, "sha256": rg.raw_sha256(data)}
        entry["packages"] = len(document["packages"])
        entries.append(entry)
    locks = {}
    for relative in LOCK_FILES:
        path = Path(repo_root) / relative
        locks[relative] = rg.raw_sha256(path.read_bytes()) if path.is_file() else None
    index_document = {"schemaVersion": 1, "tool": {"name": "syft", "version": version, "binarySha256": binary_digest},
                      "dependencyLocks": locks, "entries": entries}
    (out / SBOM_INDEX).write_bytes(rg.canonical_bytes(index_document))
    os.chmod(out / SBOM_INDEX, 0o640)
    return index_document


# ------------------------------------------------------------------ SBOM validation

def validate_sbom_dir(candidate_lock, sbom_dir, lock_tools=None, now=None, max_age_hours=None):
    """Semantic check of an SBOM directory against a candidate lock. Returns (index, {artifact sha: spdx document})."""
    lock_bytes = Path(candidate_lock).read_bytes()
    lock = rg.load_record("candidate-lock", lock_bytes, canonical=False)
    sbom_path = Path(sbom_dir)
    index = load_json(sbom_path / SBOM_INDEX, "the SBOM index")
    findings = []
    if not isinstance(index, dict) or index.get("schemaVersion") != 1 or "entries" not in index:
        raise Refused("the SBOM index is not a schemaVersion 1 object with entries")
    tools = lock_tools or load_json(TOOLS_LOCK, "ci/tools.lock")["tools"]
    syft_version = tools["syft"]["version"]
    if (index.get("tool") or {}).get("name") != "syft" or index["tool"].get("version") != syft_version:
        findings.append(f"the index was not made by the locked syft {syft_version}")
    locked_digest = tools["syft"]["platforms"]["linux-amd64"]["binarySha256"]
    if (index.get("tool") or {}).get("binarySha256") != locked_digest:
        findings.append("the index names a syft binary that is not the locked one")
    by_digest = {}
    for entry in index["entries"]:
        by_digest[entry["artifactSha256"]] = entry
    documents = {}
    # The SBOM files (and their index) are themselves payload; they describe the other artifacts and need no SBOM of their own.
    expected_artifacts = {path: digest.split(":", 1)[1] for path, digest in lock["artifacts"].items() if not path.startswith(SBOM_PAYLOAD_DIR + "/")}
    for path, sha in sorted(expected_artifacts.items()):
        entry = by_digest.get(sha)
        if entry is None:
            findings.append(f"{path}: no SBOM index entry for artifact {sha[:12]}")
            continue
        if entry["artifactPath"] != path:
            findings.append(f"{path}: the index entry is for {entry['artifactPath']}")
        if entry["kind"] in SBOM_KINDS and entry["sbom"] is None:
            findings.append(f"{path}: an {entry['kind']} must have an SBOM")
        if entry["sbom"] is None:
            continue
        file_path = sbom_path / entry["sbom"]["path"]
        if not file_path.is_file() or file_path.is_symlink():
            findings.append(f"{path}: the SBOM file {entry['sbom']['path']} is missing")
            continue
        data = file_path.read_bytes()
        if rg.raw_sha256(data) != entry["sbom"]["sha256"]:
            findings.append(f"{path}: the SBOM bytes differ from the index")
            continue
        document = json.loads(data)
        problems = validate_spdx(document, f"telos-artifact-{sha}", syft_version)
        findings += [f"{path}: {problem}" for problem in problems]
        documents[sha] = document
        if max_age_hours is not None:
            created = parse_time((document.get("creationInfo") or {}).get("created", "")[:19] + "Z")
            if (now or now_utc()) - created > datetime.timedelta(hours=max_age_hours):
                findings.append(f"{path}: the SBOM is older than {max_age_hours} hours")
    for sha in by_digest:
        if sha not in expected_artifacts.values():
            findings.append(f"the index describes artifact {sha[:12]}, which the candidate does not contain")
    if findings:
        raise Failed(findings)
    return index, documents


# ------------------------------------------------------------------ scan

def database_state(cache_dir):
    """Digest and retrieval time of the Trivy vulnerability database in ``cache_dir``."""
    db_dir = Path(cache_dir) / "db"
    try:
        metadata = json.loads((db_dir / "metadata.json").read_text(encoding="utf-8"))
        digest = rg.raw_sha256((db_dir / "trivy.db").read_bytes())
    except (OSError, ValueError):
        raise Refused("the Trivy database is missing; download it first")
    downloaded = metadata.get("DownloadedAt") or metadata.get("UpdatedAt")
    try:
        moment = datetime.datetime.fromisoformat(str(downloaded).replace("Z", "+00:00"))
    except ValueError:
        raise Refused("the Trivy database metadata has no readable timestamp")
    return {"digest": "sha256:" + digest, "retrievedAt": stamp(moment.astimezone(datetime.timezone.utc))}


def extract_oci_layout(archive, scratch):
    """Trivy reads an OCI image only as a layout DIRECTORY; the builder archives third-party images as OCI-archive tarballs.

    Returns the extracted directory when ``archive`` is a tar with ``oci-layout`` and ``index.json`` at its top, else None (a
    docker archive, or not a tar at all, is scanned as it is). Extraction is defensive even though the archive's digest was
    already checked against the lock: regular files and directories only, nothing outside the target, no links or devices.
    """
    import tarfile
    try:
        bundle = tarfile.open(archive, "r:*")
    except (tarfile.TarError, OSError):
        return None
    with bundle:
        names = bundle.getnames()
        if "oci-layout" not in names or "index.json" not in names:
            return None
        target = Path(scratch) / "oci-layout"
        target.mkdir()
        base = target.resolve()
        for member in bundle.getmembers():
            destination = (target / member.name).resolve()
            if not (member.isfile() or member.isdir()) or os.path.isabs(member.name) or base not in (destination, *destination.parents):
                raise Refused(f"{Path(archive).name}: the archive holds an entry that is not a plain file or directory inside it ({member.name[:80]})")
        bundle.extractall(target, filter="data")
    return target


def scan(candidate_lock, artifact_root, sbom_dir, tools_dir, source_dir=None, run=run_tool, binary=verified_binary, now=None,
         policy_path=LICENSE_POLICY, acceptances_path=RISK_ACCEPTANCES, cache_dir=None):
    """Scan a frozen (or rehearsal) artifact graph. Returns (report, findings); raises Refused when a scan could not run."""
    now = now or now_utc()
    lock_bytes = Path(candidate_lock).read_bytes()
    lock = rg.load_record("candidate-lock", lock_bytes, canonical=False)
    lock_digest = rg.digest_bytes(lock_bytes)
    policy = validate_license_policy(load_json(policy_path, "the license policy"))
    acceptances = validate_acceptances(load_json(acceptances_path, "the risk acceptances"), now)
    tools_lock = load_json(TOOLS_LOCK, "ci/tools.lock")["tools"]
    try:
        index, documents = validate_sbom_dir(candidate_lock, sbom_dir, tools_lock, now)
    except Failed as error:
        return None, error.findings
    findings, report_artifacts, accepted_all = [], [], []
    gitleaks, gitleaks_version, gitleaks_digest = binary(tools_dir, "gitleaks")
    trivy, trivy_version, trivy_digest = binary(tools_dir, "trivy")
    cache = cache_dir or os.path.join(tools_dir, "trivy-cache")
    refresh = run([trivy, "image", "--download-db-only", "--cache-dir", cache, "-q"], timeout=900)
    if refresh.returncode != 0:
        raise Refused(f"the Trivy database could not be refreshed: {refresh.stderr.strip()[-200:]}")
    database = database_state(cache)
    started = now
    age = (started - parse_time(database["retrievedAt"])).total_seconds()
    if age > MAX_DB_AGE_SECONDS:
        findings.append(f"the vulnerability database is {int(age // 3600)} hours old (limit 24)")
    # Secrets: the exact source tree of the candidate, never the working tree.
    secret_count = None
    if source_dir:
        secret_report = Path(tempfile.mkdtemp(prefix="gitleaks.")) / "report.json"
        completed = run([gitleaks, "dir", str(source_dir), "--no-banner", "--report-format", "json", "--report-path", str(secret_report), "--exit-code", "0"], timeout=1800)
        if completed.returncode != 0:
            raise Refused(f"gitleaks failed: {completed.stderr.strip()[-200:]}")
        leaks = json.loads(secret_report.read_text(encoding="utf-8") or "[]")
        findings += evaluate_secrets(leaks)
        secret_count = len(leaks)
    root = Path(artifact_root)
    expected = {path: digest.split(":", 1)[1] for path, digest in lock["artifacts"].items()}
    kinds = {e["artifactPath"]: e["kind"] for e in index["entries"]}
    for path, sha in sorted(expected.items()):
        if kinds.get(path) not in SBOM_KINDS:
            continue
        scratch = Path(tempfile.mkdtemp(prefix="trivy."))
        try:
            output = scratch / "result.json"
            if kinds[path] == "oci-image":
                layout = extract_oci_layout(root / path, scratch)
                target = ["image", "--input", str(layout or (root / path))]
            else:
                target = ["rootfs", str(root / path)]
            completed = run([trivy, *target, "--cache-dir", cache, "--skip-db-update", "--severity", "HIGH,CRITICAL", "--format", "json", "--output", str(output), "-q"], timeout=3600)
            if completed.returncode != 0:
                raise Refused(f"trivy failed for {path}: {completed.stderr.strip()[-200:]}")
            vulnerability_findings, accepted = evaluate_vulnerabilities(json.loads(output.read_text(encoding="utf-8")), sha, acceptances, now)
            findings += [f"{path}: {item}" for item in vulnerability_findings]
            accepted_all += [dict(item, artifact=path) for item in accepted]
            report_artifacts.append({"path": path, "sha256": sha, "highAccepted": len(accepted), "blocking": len(vulnerability_findings)})
        finally:
            shutil.rmtree(scratch, ignore_errors=True)
    # Licenses: linked dependencies are judged, isolated services and base layers inventoried.
    license_report, ours = {}, {}
    for path, sha in sorted(expected.items()):
        document = documents.get(sha)
        if document is None:
            continue
        isolated = next((k for k in policy["isolatedServices"] if k in Path(path).name), None)
        problems, inventory = evaluate_licenses(document, policy, isolated_name=isolated)
        findings += [f"{path}: {item}" for item in problems]
        license_report[path] = {"class": "isolated-service" if isolated else "telos", "service": isolated, "licenses": inventory}
        if not isolated:
            ours[path] = document
    findings += isolated_code_in_linked(ours, list(policy["isolatedServices"]), policy)
    report = {
        "schemaVersion": 1, "candidateLockDigest": lock_digest, "startedAt": stamp(started), "finishedAt": stamp(now_utc()),
        "tools": {"trivy": {"version": trivy_version, "binarySha256": trivy_digest}, "gitleaks": {"version": gitleaks_version, "binarySha256": gitleaks_digest},
                  "syft": index["tool"]},
        "vulnerabilityDatabase": database, "secretFindings": secret_count, "artifacts": report_artifacts, "licenses": license_report,
        "acceptancesUsed": accepted_all, "status": "failed" if findings else "passed", "findings": findings[:200],
    }
    return report, findings


def write_scan_result(result_out, mode, commit, lock_digest, report, findings, lock_bytes):
    """The closed gate-result record for the scan, with the report (and, when it passed, the artifact subject)."""
    out_dir = Path(result_out).resolve().parent
    attachments = []
    if report is not None:
        data = rg.canonical_bytes(report)
        rg.write_atomic_exclusive(str(out_dir / "scan-report.json"), data)
        attachments.append({"path": "scan-report.json", "sha256": rg.raw_sha256(data), "kind": "report"})
    status = "failed" if findings else "passed"
    record = {"schemaVersion": 1, "recordKind": mode, "gateId": os.environ.get("TELOS_GATE_ID", "candidate-supply-chain"), "sourceCommit": commit,
              "candidateLockDigest": lock_digest, "status": status,
              "reason": ("scan passed" if not findings else "failed: " + "; ".join(f[:100] for f in findings[:3]))[:900],
              "subjectFile": None, "attachments": attachments, "toolVersions": {"python3": sys.version.split()[0]}}
    if status == "passed":
        lock = json.loads(lock_bytes)
        index_digest = lock["artifactIndexDigest"]
        subject = {"schemaVersion": 1, "kind": "artifact", "sourceCommit": commit, "candidateLockDigest": lock_digest, "selector": "artifact-index", "artifactDigest": index_digest}
        subject_bytes = rg.canonical_bytes(subject)
        rg.write_atomic_exclusive(str(out_dir / "subject.json"), subject_bytes)
        attachments.append({"path": "subject.json", "sha256": rg.raw_sha256(subject_bytes), "kind": "subject"})
        record["subjectFile"] = "subject.json"
    attachments.sort(key=lambda item: item["path"])
    rg.reject_secret_text(record["reason"], "reason")
    rg.validate_record("gate-result", record)
    rg.write_atomic_exclusive(str(result_out), rg.canonical_bytes(record))
    return status


# ------------------------------------------------------------------ policy self-check

def check_policy():
    lock_module = None
    import importlib.util
    spec = importlib.util.spec_from_file_location("bootstrap_ci_tools", ROOT / "scripts" / "bootstrap-ci-tools.py")
    lock_module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(lock_module)
    try:
        lock_module.load_lock(TOOLS_LOCK)
    except lock_module.Refused as error:
        raise Refused(str(error))
    validate_license_policy(load_json(LICENSE_POLICY, "ci/license-policy.json"))
    validate_acceptances(load_json(RISK_ACCEPTANCES, "ci/risk-acceptances.json"))
    schema = load_json(ROOT / "ci" / "risk-acceptance.schema.json", "ci/risk-acceptance.schema.json")
    described = set((((schema.get("properties") or {}).get("acceptances") or {}).get("items") or {}).get("properties") or {})
    if described != ACCEPTANCE_FIELDS:
        raise Refused("ci/risk-acceptance.schema.json does not describe exactly the fields the validator enforces")
    return True


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("policy")
    p = sub.add_parser("generate-sbom")
    for flag in ("payload-index", "artifact-root", "out-dir", "tools-dir"):
        p.add_argument(f"--{flag}", required=True)
    p.add_argument("--root", default=str(ROOT))
    p = sub.add_parser("validate-sbom")
    p.add_argument("--candidate-lock", required=True)
    p.add_argument("--sbom-dir", required=True)
    p.add_argument("--max-age-hours", type=int)
    p = sub.add_parser("scan")
    for flag in ("artifact-root", "sbom-dir", "result-out", "tools-dir"):
        p.add_argument(f"--{flag}", required=True)
    group = p.add_mutually_exclusive_group(required=True)
    group.add_argument("--candidate-lock")
    group.add_argument("--rehearsal-lock", help="rehearsal: the lock of the rehearsal artifact graph to scan (needs --source-commit)")
    p.add_argument("--source-commit")
    p.add_argument("--source-dir")
    args = parser.parse_args(argv)
    try:
        if args.command == "policy":
            check_policy()
            print("supply-chain: tool lock, license policy and risk acceptances are valid")
        elif args.command == "generate-sbom":
            result = generate_sbom(args.payload_index, args.artifact_root, args.out_dir, args.tools_dir, args.root)
            print(f"supply-chain: {sum(1 for e in result['entries'] if e['sbom'])} SBOMs written; {sum(1 for e in result['entries'] if e['exempt'])} artifacts exempt")
        elif args.command == "validate-sbom":
            validate_sbom_dir(args.candidate_lock, args.sbom_dir, max_age_hours=args.max_age_hours)
            print("supply-chain: every artifact has a valid, bound SBOM")
        else:
            if args.candidate_lock and args.source_commit:
                raise Refused("--source-commit belongs to a rehearsal only")
            lock_path = args.candidate_lock or args.rehearsal_lock
            lock_bytes = Path(lock_path).read_bytes()
            lock = json.loads(lock_bytes)
            mode = "candidate" if args.candidate_lock else "rehearsal"
            commit = lock["sourceCommit"]
            if mode == "rehearsal" and args.source_commit and args.source_commit != commit:
                raise Refused("--source-commit differs from the rehearsal lock's sourceCommit")
            lock_digest = rg.digest_bytes(lock_bytes)
            report, findings = scan(lock_path, args.artifact_root, args.sbom_dir, args.tools_dir, args.source_dir)
            status = write_scan_result(args.result_out, mode, commit, lock_digest, report, findings, lock_bytes)
            if status != "passed":
                raise Failed(findings)
            print(f"supply-chain: scan passed for {len(report['artifacts'])} artifacts")
    except Failed as error:
        for finding in error.findings[:50]:
            print(f"supply-chain: {finding}", file=sys.stderr)
        print(f"supply-chain: FAILED ({len(error.findings)} findings)", file=sys.stderr)
        return EXIT_FAILED
    except (Refused, rg.GraphError) as error:
        print(f"supply-chain: {error}", file=sys.stderr)
        return EXIT_USAGE
    return 0


if __name__ == "__main__":
    sys.exit(main())
