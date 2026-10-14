#!/usr/bin/env python3
"""The release builder against a miniature repository and stand-in podman/skopeo.

The stand-ins write real OCI archives whose digests follow from their content, so the builder's own
verification (blob hashes, manifest digest, labels, platform) is exercised for real; only the
container engine and registry are replaced. Each rejection test breaks exactly one input.
"""

import hashlib
import io
import json
import os
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile
import textwrap
import unittest
from pathlib import Path

sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts" / "lib"))
import build_release as br  # noqa: E402
import release_compose as rc  # noqa: E402
import release_graph as rg  # noqa: E402

FAKE_OCI = '''
import hashlib, io, json, os, sys, tarfile

def sha(data):
    return hashlib.sha256(data).hexdigest()

def archive(path, ref, layer_seed, labels, epoch, arch="amd64", os_name="linux"):
    layer = tarfile.TarInfo("app"); data = layer_seed.encode()
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode="w") as tar:
        layer.size = len(data); layer.mtime = int(epoch); tar.addfile(layer, io.BytesIO(data))
    layer_bytes = buffer.getvalue()
    config = json.dumps({"architecture": arch, "os": os_name, "created": str(epoch), "config": {"Labels": labels, "User": "10001:10001"},
                         "rootfs": {"type": "layers", "diff_ids": ["sha256:" + sha(layer_bytes)]}}, sort_keys=True).encode()
    manifest = json.dumps({"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json",
                           "config": {"mediaType": "application/vnd.oci.image.config.v1+json", "digest": "sha256:" + sha(config), "size": len(config)},
                           "layers": [{"mediaType": "application/vnd.oci.image.layer.v1.tar", "digest": "sha256:" + sha(layer_bytes), "size": len(layer_bytes)}]},
                          sort_keys=True).encode()
    index = json.dumps({"schemaVersion": 2, "manifests": [{"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": "sha256:" + sha(manifest),
                        "size": len(manifest), "annotations": {"org.opencontainers.image.ref.name": ref}}]}, sort_keys=True).encode()
    with tarfile.open(path, "w") as tar:
        for name, blob in (("oci-layout", b'{"imageLayoutVersion":"1.0.0"}'), ("index.json", index), ("blobs/sha256/" + sha(manifest), manifest),
                           ("blobs/sha256/" + sha(config), config), ("blobs/sha256/" + sha(layer_bytes), layer_bytes)):
            info = tarfile.TarInfo(name); info.size = len(blob); info.mtime = 0; tar.addfile(info, io.BytesIO(blob))
    return "sha256:" + sha(manifest)

def third_party_digest(repository):
    import tempfile
    with tempfile.NamedTemporaryFile() as handle:
        return archive(handle.name, "x", "third-party " + repository, {}, 0)
'''


def write_fakes(directory):
    directory.mkdir(parents=True, exist_ok=True)
    (directory / "fakeoci.py").write_text(FAKE_OCI, encoding="utf-8")
    podman = textwrap.dedent('''\
        #!/usr/bin/env python3
        import hashlib, json, os, sys
        sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
        import fakeoci
        args = sys.argv[1:]
        state = os.environ["TELOS_BUILD_STATE"]
        os.makedirs(state, exist_ok=True)
        def flag(name):
            return args[args.index(name) + 1] if name in args else None
        if args[0] == "build":
            tag, dockerfile = flag("-t"), flag("-f")
            labels = dict(a.split("=", 1) for a, prev in zip(args[1:], args) if prev == "--label")
            if os.environ.get("TELOS_BUILD_FAKE_DROP_LABELS"):
                labels = {}
            seed = open(dockerfile).read() + "|" + tag
            if os.environ.get("TELOS_BUILD_NONDETERMINISM") and "--no-cache" in args:
                seed += "|" + os.urandom(8).hex()
            with open(os.path.join(state, hashlib.sha256(tag.encode()).hexdigest()), "w") as handle:
                json.dump({"seed": seed, "labels": labels, "epoch": flag("--timestamp")}, handle)
        elif args[0] == "save":
            tag, out = args[-1], flag("-o")
            record = json.load(open(os.path.join(state, hashlib.sha256(tag.encode()).hexdigest())))
            fakeoci.archive(out, tag, record["seed"], record["labels"], record["epoch"])
        else:
            sys.exit("fake podman: unsupported " + " ".join(args))
        ''')
    skopeo = textwrap.dedent('''\
        #!/usr/bin/env python3
        import os, sys
        sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
        import fakeoci
        args = sys.argv[1:]
        assert args[0] == "copy" and "--preserve-digests" in args, args
        source, destination = args[-2], args[-1]
        repository = source[len("docker://"):].split("@")[0]
        path, _, ref = destination[len("oci-archive:"):].partition(":")
        if os.environ.get("TELOS_BUILD_FAKE_WRONG_DIGEST"):
            repository += "-tampered"
        fakeoci.archive(path, ref, "third-party " + repository, {}, 0)
        ''')
    for name, body in (("podman", podman), ("skopeo", skopeo)):
        (directory / name).write_text(body, encoding="utf-8")
        os.chmod(directory / name, 0o755)
    sys.path.insert(0, str(directory))
    import importlib
    import fakeoci
    importlib.reload(fakeoci)
    return fakeoci


COMPOSE = """\
services:
  telos-migrate:
    build:
      context: .
      dockerfile: backend/Dockerfile
    container_name: telos-migrate
  telos-sanitize-restore:
    profiles: ["restore"]
    build:
      context: .
      dockerfile: backend/Dockerfile
    container_name: telos-sanitize-restore
  telos-audiobook-migrate:
    profiles: ["migration"]
    build:
      context: .
      dockerfile: backend/Dockerfile
    container_name: telos-audiobook-migrate
  telos-core:
    build:
      context: .
      dockerfile: backend/Dockerfile
    container_name: telos-core
  telos-client:
    build:
      context: ./frontend
      dockerfile: Dockerfile
    container_name: telos-client
  telos-egress-proxy:
    build:
      context: .
      dockerfile: deploy/egress-proxy/Dockerfile
    container_name: telos-egress-proxy
  redis:
    image: docker.io/library/redis:7-alpine@sha256:%(redis_index)s
    container_name: telos-redis
"""
SETUP = """\
services:
  telos-setup-proxy:
    build:
      context: .
      dockerfile: backend/Dockerfile
    container_name: telos-setup-proxy
"""


class Fixture(unittest.TestCase):
    version = "0.1.0-beta.1"

    @classmethod
    def setUpClass(cls):
        cls.shared = Path(tempfile.mkdtemp(prefix="build-release-shared-"))
        cls.fakes = cls.shared / "bin"
        cls.fakeoci = write_fakes(cls.fakes)

    @classmethod
    def tearDownClass(cls):
        shutil.rmtree(cls.shared, True)

    def setUp(self):
        self.dir = Path(tempfile.mkdtemp(prefix="build-release-"))
        self.addCleanup(shutil.rmtree, self.dir, True)
        self.repo = self.dir / "repo"
        self.make_repo()
        os.environ["TELOS_BUILD_PODMAN"] = str(self.fakes / "podman")
        os.environ["TELOS_BUILD_SKOPEO"] = str(self.fakes / "skopeo")
        os.environ["TELOS_BUILD_STATE"] = str(self.dir / "podman-state")
        for name in ("TELOS_BUILD_FAKE_DROP_LABELS", "TELOS_BUILD_FAKE_WRONG_DIGEST", "TELOS_BUILD_NONDETERMINISM"):
            os.environ.pop(name, None)
        self.addCleanup(lambda: [os.environ.pop(k, None) for k in ("TELOS_BUILD_PODMAN", "TELOS_BUILD_SKOPEO", "TELOS_BUILD_STATE")])

    # ---- fixture repository
    def write(self, relative, text, mode=None):
        path = self.repo / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")
        if mode:
            os.chmod(path, mode)

    def make_repo(self, version=None):
        version = version or self.version
        self.repo.mkdir()
        git = lambda *a: subprocess.run(["git", "-C", str(self.repo), *a], check=True, capture_output=True)
        git("init", "-q", "-b", "main")
        git("config", "user.email", "t@example.invalid")
        git("config", "user.name", "T")
        redis_platform = self.fakeoci.third_party_digest("docker.io/library/redis")
        redis_index = "sha256:" + "ab" * 32
        self.write("docker-compose.yml", COMPOSE % {"redis_index": redis_index.split(":")[1]})
        self.write("docker-compose.setup.yml", SETUP)
        self.write("docker-compose.private-ca.yml", "services: {}\n")
        shutil.copy(ROOT / "docker-compose.release.yml", self.repo / "docker-compose.release.yml")
        lock = {"schemaVersion": 2, "images": {"redis": {"manifestDigest": redis_index, "platforms": {"linux/amd64": redis_platform},
                "registry": "docker.io", "resolvedAt": "2026-10-13T00:00:00Z", "source": "docker.io/library/redis:7-alpine"}}, "testImages": {}, "toolImages": {}}
        self.write("release/images.lock", json.dumps(lock, indent=2) + "\n")
        self.write("release/generation-compat.json", json.dumps({"schemaVersion": 1, "minSchema": 0, "rollbackCompatibleThrough": 0}) + "\n")
        self.write("release/predecessor-policy.json", json.dumps({"schemaVersion": 1, "predecessor": None, "maxSchemaMigration": 2, "allowInPlaceRollback": True}) + "\n")
        self.write("release/telos-tag-signers", "# no signer enrolled\n")
        self.write("backend/Dockerfile", "FROM scratch\n# core\n")
        self.write("backend/go.sum", "example.com/m v1 h1:x\n")
        self.write("backend/db/migrations/0001_first.sql", "SELECT 1;\n")
        self.write("backend/db/migrations/0002_second.sql", "SELECT 2;\n")
        self.write("frontend/Dockerfile", "FROM scratch\n# client\n")
        self.write("frontend/package.json", json.dumps({"name": "x", "version": version}) + "\n")
        self.write("frontend/package-lock.json", "{}\n")
        self.write("frontend/src-tauri/Cargo.toml", f'[package]\nname = "x"\nversion = "{version}"\n')
        self.write("frontend/src-tauri/Cargo.lock", "# lock\n")
        self.write("frontend/src-tauri/tauri.conf.json", json.dumps({"version": version}) + "\n")
        self.write("deploy/egress-proxy/Dockerfile", "FROM scratch\n# egress\n")
        self.write("deploy/postgres/init.sh", "#!/bin/sh\ntrue\n", 0o755)
        self.write("scripts/verify-image-lock.py", "import sys\nsys.exit(0)\n")
        self.write("scripts/verify-desktop-artifact.py", "import sys\nsys.exit(int(__import__('os').environ.get('FAKE_DESKTOP_EXIT', '0')))\n")
        self.write("scripts/backup.sh", "#!/bin/sh\ntrue\n", 0o755)
        self.write("scripts/tests/skip-me.sh", "#!/bin/sh\n")
        self.write("cli/commands/start.sh", "run() { :; }\n")
        self.write("config/traefik.yaml", "entryPoints: {}\n")
        self.write("documentation/operations/runbook.md", "# runbook\n")
        self.write("telos", "#!/usr/bin/env bash\ntrue\n", 0o755)
        self.write("install.sh", "#!/usr/bin/env bash\ntrue\n", 0o755)
        self.write("LICENSE", "Apache\n")
        self.write("NOTICE", "notice\n")
        self.write(".env.example", "TELOS_DOMAIN=\n")
        git("add", "-A")
        git("commit", "-q", "-m", "fixture")
        self.commit = git("rev-parse", "HEAD").stdout.decode().strip()

    def desktop(self, commit=None, version=None):
        self.desktops = getattr(self, "desktops", 0) + 1
        directory = self.dir / f"desktop-{self.desktops}"
        directory.mkdir()
        image = directory / f"Telos_{version or self.version}_amd64.AppImage"
        image.write_bytes(b"\x7fELF" + b"appimage" * 50)
        identity = {"schemaVersion": 1, "platform": "linux-x86_64", "size": image.stat().st_size,
                    "sha256": hashlib.sha256(image.read_bytes()).hexdigest(), "sourceCommit": commit or self.commit,
                    "version": version or self.version, "workflowRunId": 1, "workflowRunAttempt": 1}
        (directory / (image.name + ".identity.json")).write_bytes(rg.canonical_bytes(identity))
        return directory

    # ---- builder driving
    def args(self, **overrides):
        values = dict(version=self.version, tag=None, out_dir=str(self.dir / "out"), desktop_dir=str(self.desktop()), rehearsal=True,
                      source_commit=self.commit, verify_reproducible=False, tools_dir=None, rollback_compatible_through=None,
                      predecessor_id=None, repo=str(self.repo))
        values.update(overrides)
        return type("Args", (), values)()

    def fake_sbom(self, index_path, root, out_dir, tools_dir):
        out_dir = Path(out_dir)
        out_dir.mkdir(mode=0o750, exist_ok=True)
        index = json.loads(Path(index_path).read_bytes())
        entries = []
        for artifact in index["artifacts"]:
            entry = {"artifactPath": artifact["path"], "artifactSha256": artifact["sha256"], "kind": artifact["kind"], "sbom": None, "exempt": "n/a", "packages": 0}
            if artifact["kind"] in ("oci-image", "desktop"):
                document = {"spdxVersion": "SPDX-2.3", "creationInfo": {"created": "2099-01-01T00:00:00Z"}, "documentNamespace": "random-" + os.urandom(4).hex(),
                            "name": f"telos-artifact-{artifact['sha256']}", "packages": [{"name": "p"}]}
                data = rg.canonical_bytes(document)
                (out_dir / f"{artifact['sha256']}.spdx.json").write_bytes(data)
                entry.update(sbom={"path": f"{artifact['sha256']}.spdx.json", "sha256": rg.raw_sha256(data)}, exempt=None, packages=1)
            entries.append(entry)
        (out_dir / "sbom-index.json").write_bytes(rg.canonical_bytes({"schemaVersion": 1, "tool": {"name": "syft"}, "entries": entries}))

    def build(self, **overrides):
        return br.build(self.args(**overrides), tools=br.Tools(), sbom_generator=self.fake_sbom, repo=self.repo)


class BuildTests(Fixture):
    def test_a_rehearsal_builds_a_verifiable_candidate(self):
        record = self.build()
        out = self.dir / "out"
        self.assertEqual(record["recordKind"], "rehearsal")
        root = out / "candidate"
        graph = rg.verify_root(root)
        self.assertEqual(graph["candidateLockDigest"], record["candidateLockDigest"])
        self.assertEqual(graph["version"], self.version)
        self.assertEqual(graph["sourceCommit"], self.commit)
        archive = out / f"telos-{self.version}-candidate.tar.zst"
        self.assertTrue(archive.is_file())
        digest = hashlib.sha256(archive.read_bytes()).hexdigest()
        self.assertEqual((out / (archive.name + ".sha256")).read_text().strip(), f"{digest}  {archive.name}")

    def test_the_payload_has_the_documented_shape(self):
        self.build()
        root = self.dir / "out" / "candidate"
        index = json.loads((root / "artifact-index.json").read_text())
        by_path = {a["path"]: a for a in index["artifacts"]}
        for name in ("telos-core", "telos-client", "telos-egress-proxy", "redis"):
            artifact = by_path[f"images/{name}.oci.tar"]
            self.assertEqual((artifact["kind"], artifact["platform"]), ("oci-image", "linux/amd64"))
            self.assertRegex(artifact["contentDigest"], r"^sha256:[0-9a-f]{64}$")
        self.assertIn(f"source/telos-{self.version}-source.tar", by_path)
        self.assertEqual(by_path[f"desktop/Telos_{self.version}_amd64.AppImage"]["kind"], "desktop")
        self.assertEqual(by_path["generation/generation.json"]["kind"], "generation")
        self.assertTrue(any(path.startswith("sbom/") and by_path[path]["kind"] == "sbom" for path in by_path))
        self.assertNotIn("generation/scripts/tests/skip-me.sh", by_path)

    def test_third_party_images_keep_their_locked_digest(self):
        self.build()
        root = self.dir / "out" / "candidate"
        lock = json.loads((self.repo / "release" / "images.lock").read_text())
        info = br.read_oci_archive(root / "images" / "redis.oci.tar")
        self.assertEqual(info["manifestDigest"], lock["images"]["redis"]["platforms"]["linux/amd64"])

    def test_the_generation_is_self_describing_and_candidate_independent(self):
        record = self.build()
        generation = self.dir / "out" / "candidate" / "generation"
        document = rg.load_record("generation-manifest", (generation / "generation.json").read_bytes())
        self.assertNotIn(record["candidateLockDigest"].split(":")[1], (generation / "generation.json").read_text())
        self.assertEqual(document["migrations"], {"min": 0, "current": 2, "max": 2, "checksumSetDigest": document["migrations"]["checksumSetDigest"]})
        self.assertEqual(document["rollbackCompatibleThrough"], 0)
        listed = {item["path"] for item in document["artifactIndexSubset"]}
        actual = {p.relative_to(generation).as_posix() for p in generation.rglob("*") if p.is_file()} - {"generation.json"}
        self.assertEqual(listed, actual)
        for item in document["artifactIndexSubset"]:
            self.assertEqual(rg.raw_sha256((generation / item["path"]).read_bytes()), item["sha256"])

    def test_images_env_names_exactly_the_built_images_by_digest(self):
        self.build()
        root = self.dir / "out" / "candidate"
        env = dict(line.split("=", 1) for line in (root / "generation" / "images.env").read_text().splitlines())
        self.assertEqual(sorted(env), ["TELOS_CLIENT_IMAGE", "TELOS_CORE_IMAGE", "TELOS_EGRESS_PROXY_IMAGE"])
        core = br.read_oci_archive(root / "images" / "telos-core.oci.tar")["manifestDigest"]
        self.assertEqual(env["TELOS_CORE_IMAGE"], f"localhost/telos-core@{core}")
        self.assertEqual(stat.S_IMODE(os.stat(root / "generation" / "images.env").st_mode), 0o600)

    def test_the_generation_compose_model_has_no_build_key_and_verifies(self):
        self.build()
        generation = self.dir / "out" / "candidate" / "generation"
        self.assertEqual(rc.verify(generation, rc.load_lock(self.repo / "release" / "images.lock")), [])
        for name in rc.BASE_FILES:
            self.assertNotIn("build:", (generation / name).read_text())

    def test_the_images_carry_the_version_and_the_source_commit(self):
        self.build()
        root = self.dir / "out" / "candidate"
        labels = br.read_oci_archive(root / "images" / "telos-client.oci.tar")["labels"]
        self.assertEqual(labels["org.opencontainers.image.version"], self.version)
        self.assertEqual(labels["org.opencontainers.image.revision"], self.commit)

    def test_sboms_are_reproducible_and_bound_to_the_artifact(self):
        self.build()
        root = self.dir / "out" / "candidate"
        for path in sorted((root / "sbom").glob("*.spdx.json")):
            document = json.loads(path.read_text())
            self.assertEqual(document["documentNamespace"], f"https://telos.invalid/spdx/{path.name.split('.')[0]}")
            self.assertTrue(document["creationInfo"]["created"].endswith("Z"))
            self.assertNotEqual(document["creationInfo"]["created"], "2099-01-01T00:00:00Z")
        index = json.loads((root / "sbom" / "sbom-index.json").read_text())
        for entry in index["entries"]:
            if entry["sbom"]:
                self.assertEqual(rg.raw_sha256((root / "sbom" / entry["sbom"]["path"]).read_bytes()), entry["sbom"]["sha256"])

    def test_the_package_unpacks_to_the_same_verified_graph(self):
        record = self.build()
        out = self.dir / "out"
        target = self.dir / "unpacked" / "candidate"
        target.parent.mkdir()
        br.extract_package(br.Tools(), out / f"telos-{self.version}-candidate.tar.zst", target)
        self.assertEqual(rg.verify_root(target)["candidateLockDigest"], record["candidateLockDigest"])
        self.assertEqual(br.compare_candidates(out / "candidate", target), [])


class ReproducibilityTests(Fixture):
    def test_the_same_source_builds_identically_in_other_places_and_environments(self):
        first = self.build(out_dir=str(self.dir / "one"))
        saved = {key: os.environ.get(key) for key in ("TZ", "LC_ALL", "LANG")}
        os.environ.update(TZ="Pacific/Auckland", LC_ALL="de_DE.UTF-8", LANG="de_DE.UTF-8")
        old_umask = os.umask(0o077)
        try:
            second = self.build(out_dir=str(self.dir / "two" / "deeper"))
        finally:
            os.umask(old_umask)
            for key, value in saved.items():
                os.environ.pop(key, None) if value is None else os.environ.__setitem__(key, value)
        self.assertEqual(first["candidateLockDigest"], second["candidateLockDigest"])
        self.assertEqual(br.compare_candidates(self.dir / "one" / "candidate", self.dir / "two" / "deeper" / "candidate"), [])
        self.assertEqual(first["archiveSha256"], second["archiveSha256"])

    def test_a_second_no_cache_build_must_agree(self):
        record = self.build(verify_reproducible=True)
        self.assertTrue(record["reproducibilityVerified"])

    def test_a_nondeterministic_build_is_refused(self):
        os.environ["TELOS_BUILD_NONDETERMINISM"] = "1"
        with self.assertRaisesRegex(br.BuildError, "the two builds differ"):
            self.build(verify_reproducible=True)


class SourceIdentityTests(Fixture):
    def git(self, *a, check=True):
        return subprocess.run(["git", "-C", str(self.repo), *a], check=check, capture_output=True, text=True)

    def test_a_candidate_needs_an_enrolled_signer(self):
        self.git("tag", "-a", f"v{self.version}", "-m", "release")
        with self.assertRaises(br.Unavailable):
            self.build(rehearsal=False, source_commit=None)

    def test_a_lightweight_tag_is_refused(self):
        self.git("tag", f"v{self.version}")
        with self.assertRaisesRegex(br.BuildError, "lightweight"):
            self.build(rehearsal=False, source_commit=None)

    def test_a_missing_tag_is_refused(self):
        with self.assertRaisesRegex(br.BuildError, "does not exist"):
            self.build(rehearsal=False, source_commit=None)

    def test_a_signed_tag_builds_a_candidate_and_a_different_signer_does_not(self):
        key = self.dir / "key"
        subprocess.run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(key)], check=True)
        other = self.dir / "other"
        subprocess.run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(other)], check=True)
        public = (key.with_suffix(".pub")).read_text().split()
        (self.repo / "release" / "telos-tag-signers").write_text(f'release@example.invalid namespaces="git" {public[0]} {public[1]}\n')
        self.git("add", "-A")
        self.git("commit", "-q", "-m", "enroll signer")
        self.commit = self.git("rev-parse", "HEAD").stdout.strip()
        desktop = self.desktop()
        self.git("-c", "gpg.format=ssh", "-c", f"user.signingkey={other}", "tag", "-s", f"v{self.version}", "-m", "release")
        with self.assertRaisesRegex(br.BuildError, "not signed by an enrolled signer"):
            self.build(rehearsal=False, source_commit=None, desktop_dir=str(desktop))
        self.git("tag", "-d", f"v{self.version}")
        self.git("-c", "gpg.format=ssh", "-c", f"user.signingkey={key}", "tag", "-s", f"v{self.version}", "-m", "release")
        record = self.build(rehearsal=False, source_commit=None, desktop_dir=str(desktop))
        self.assertEqual(record["recordKind"], "candidate")
        self.assertEqual(record["sourceCommit"], self.commit)

    def test_a_dirty_tree_is_refused_for_a_candidate(self):
        key = self.dir / "key"
        subprocess.run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(key)], check=True)
        public = key.with_suffix(".pub").read_text().split()
        (self.repo / "release" / "telos-tag-signers").write_text(f'r@example.invalid namespaces="git" {public[0]} {public[1]}\n')
        self.git("add", "-A")
        self.git("commit", "-q", "-m", "enroll signer")
        self.git("-c", "gpg.format=ssh", "-c", f"user.signingkey={key}", "tag", "-s", f"v{self.version}", "-m", "release")
        (self.repo / "README.md").write_text("dirty\n")
        with self.assertRaisesRegex(br.BuildError, "not clean"):
            self.build(rehearsal=False, source_commit=None)

    def test_head_must_be_the_tagged_commit(self):
        key = self.dir / "key"
        subprocess.run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(key)], check=True)
        public = key.with_suffix(".pub").read_text().split()
        (self.repo / "release" / "telos-tag-signers").write_text(f'r@example.invalid namespaces="git" {public[0]} {public[1]}\n')
        self.git("add", "-A")
        self.git("commit", "-q", "-m", "enroll signer")
        self.git("-c", "gpg.format=ssh", "-c", f"user.signingkey={key}", "tag", "-s", f"v{self.version}", "-m", "release")
        self.write("later.txt", "x\n")
        self.git("add", "-A")
        self.git("commit", "-q", "-m", "later")
        with self.assertRaisesRegex(br.BuildError, "not the tagged commit"):
            self.build(rehearsal=False, source_commit=None)

    def test_a_candidate_refuses_a_source_commit_argument(self):
        with self.assertRaisesRegex(br.BuildError, "only for a rehearsal"):
            self.build(rehearsal=False)

    def test_a_rehearsal_needs_a_full_known_commit(self):
        with self.assertRaisesRegex(br.BuildError, "40-character"):
            self.build(source_commit="abc123")
        with self.assertRaisesRegex(br.BuildError, "not in this repository"):
            self.build(source_commit="0" * 40)

    def test_tag_and_version_must_agree(self):
        with self.assertRaisesRegex(br.BuildError, "does not match version"):
            self.build(tag="v9.9.9-beta.1")
        with self.assertRaisesRegex(br.BuildError, "not a beta version"):
            self.build(version="1.0.0")


class RejectionTests(Fixture):
    def commit_change(self, relative, text):
        self.write(relative, text)
        subprocess.run(["git", "-C", str(self.repo), "add", "-A"], check=True)
        subprocess.run(["git", "-C", str(self.repo), "commit", "-q", "-m", "change"], check=True)
        self.commit = subprocess.run(["git", "-C", str(self.repo), "rev-parse", "HEAD"], check=True, capture_output=True, text=True).stdout.strip()

    def test_a_version_declaration_that_disagrees_is_refused(self):
        self.commit_change("frontend/package.json", json.dumps({"version": "0.2.0-beta.1"}) + "\n")
        with self.assertRaisesRegex(br.BuildError, r"frontend/package.json declares version"):
            self.build(source_commit=self.commit)

    def test_a_mutable_or_unlocked_image_is_refused(self):
        compose = (self.repo / "docker-compose.yml").read_text().replace("redis:7-alpine@", "redis:8-alpine@")
        self.commit_change("docker-compose.yml", compose)
        with self.assertRaisesRegex(rc.ComposeError, "not in release/images.lock"):
            self.build(source_commit=self.commit)

    def test_a_third_party_archive_that_is_not_the_locked_digest_is_refused(self):
        os.environ["TELOS_BUILD_FAKE_WRONG_DIGEST"] = "1"
        with self.assertRaisesRegex(br.BuildError, "is not the locked platform digest"):
            self.build()

    def test_images_without_version_and_source_labels_are_refused(self):
        os.environ["TELOS_BUILD_FAKE_DROP_LABELS"] = "1"
        with self.assertRaisesRegex(br.BuildError, "version and source commit as labels"):
            self.build()

    def test_a_candidate_without_the_desktop_artifact_is_refused(self):
        work = self.dir / "w"
        work.mkdir()
        source = br.Source(self.commit, 1, f"v{self.version}", self.version, False)
        with self.assertRaisesRegex(br.BuildError, "native desktop artifact"):
            br.build_once(self.args(desktop_dir=None, rehearsal=False), br.Tools(), self.fake_sbom, source, work, False)

    def test_a_desktop_artifact_from_another_commit_or_version_is_refused(self):
        with self.assertRaisesRegex(br.BuildError, "different commit"):
            self.build(desktop_dir=str(self.desktop(commit="1" * 40)))

    def test_a_desktop_artifact_for_another_version_is_refused(self):
        with self.assertRaisesRegex(br.BuildError, "different version"):
            self.build(desktop_dir=str(self.desktop(version="0.1.0-beta.2")))

    def test_a_desktop_artifact_the_inspector_rejects_is_refused(self):
        os.environ["FAKE_DESKTOP_EXIT"] = "1"
        self.addCleanup(os.environ.pop, "FAKE_DESKTOP_EXIT", None)
        with self.assertRaisesRegex(br.BuildError, "was rejected"):
            self.build()

    def test_an_uninspectable_desktop_artifact_is_not_run(self):
        os.environ["FAKE_DESKTOP_EXIT"] = "3"
        self.addCleanup(os.environ.pop, "FAKE_DESKTOP_EXIT", None)
        with self.assertRaises(br.Unavailable):
            self.build()

    def test_two_appimages_are_refused(self):
        directory = self.desktop()
        (directory / "Telos_extra_amd64.AppImage").write_bytes(b"x")
        with self.assertRaisesRegex(br.BuildError, "exactly one"):
            self.build(desktop_dir=str(directory))

    def test_a_symlink_in_the_generation_is_refused(self):
        os.symlink("/etc/passwd", self.repo / "config" / "link")
        subprocess.run(["git", "-C", str(self.repo), "add", "-A"], check=True)
        subprocess.run(["git", "-C", str(self.repo), "commit", "-q", "-m", "link"], check=True)
        commit = subprocess.run(["git", "-C", str(self.repo), "rev-parse", "HEAD"], check=True, capture_output=True, text=True).stdout.strip()
        with self.assertRaisesRegex(br.BuildError, "symbolic link"):
            self.build(source_commit=commit, desktop_dir=str(self.desktop(commit=commit)))

    def test_a_secret_in_the_generation_is_refused(self):
        self.commit_change("config/leak.txt", "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n")
        with self.assertRaisesRegex(br.BuildError, "secret-looking material"):
            self.build(source_commit=self.commit, desktop_dir=str(self.desktop(commit=self.commit)))

    def test_local_state_files_are_refused(self):
        self.commit_change("config/.env", "SECRET=1\n")
        with self.assertRaisesRegex(br.BuildError, "must not be shipped"):
            self.build(source_commit=self.commit, desktop_dir=str(self.desktop(commit=self.commit)))

    def test_a_missing_compat_declaration_is_refused(self):
        subprocess.run(["git", "-C", str(self.repo), "rm", "-q", "release/generation-compat.json"], check=True)
        subprocess.run(["git", "-C", str(self.repo), "commit", "-q", "-m", "drop"], check=True)
        commit = subprocess.run(["git", "-C", str(self.repo), "rev-parse", "HEAD"], check=True, capture_output=True, text=True).stdout.strip()
        with self.assertRaisesRegex(br.BuildError, "generation-compat.json is missing"):
            self.build(source_commit=commit, desktop_dir=str(self.desktop(commit=commit)))

    def test_a_migration_gap_is_refused(self):
        subprocess.run(["git", "-C", str(self.repo), "rm", "-q", "backend/db/migrations/0001_first.sql"], check=True)
        subprocess.run(["git", "-C", str(self.repo), "commit", "-q", "-m", "gap"], check=True)
        commit = subprocess.run(["git", "-C", str(self.repo), "rev-parse", "HEAD"], check=True, capture_output=True, text=True).stdout.strip()
        with self.assertRaisesRegex(br.BuildError, "not contiguous"):
            self.build(source_commit=commit, desktop_dir=str(self.desktop(commit=commit)))

    def test_the_output_directory_rules(self):
        with self.assertRaisesRegex(br.BuildError, "absolute"):
            self.build(out_dir="relative/out")
        with self.assertRaisesRegex(br.BuildError, "inside the repository"):
            self.build(out_dir=str(self.repo / "out"))
        occupied = self.dir / "occupied"
        occupied.mkdir()
        (occupied / "x").write_text("x")
        with self.assertRaisesRegex(br.BuildError, "empty or absent"):
            self.build(out_dir=str(occupied))

    def test_a_changed_payload_after_indexing_fails_verification(self):
        self.build()
        root = self.dir / "out" / "candidate"
        victim = root / "generation" / "scripts" / "backup.sh"
        victim.write_bytes(victim.read_bytes() + b"# tampered\n")
        with self.assertRaises(rg.GraphError):
            rg.verify_root(root)

    def test_predecessors_and_rollback_bound_flow_into_the_generation(self):
        self.build(predecessor_id=["sha256:" + "7" * 64], rollback_compatible_through=1)
        document = json.loads((self.dir / "out" / "candidate" / "generation" / "generation.json").read_text())
        self.assertEqual(document["predecessorIds"], ["sha256:" + "7" * 64])
        self.assertEqual(document["rollbackCompatibleThrough"], 1)


class BuildPlanTests(unittest.TestCase):
    def test_the_build_plan_matches_the_source_compose_files(self):
        import yaml
        built = {}
        for name in rc.BASE_FILES:
            for service, body in (yaml.safe_load((ROOT / name).read_text())["services"] or {}).items():
                if "build" in body:
                    built[service] = body["build"]
        self.assertEqual(set(built) - set(br.BUILD_PLAN), {s for s in built if rc.BUILT_SERVICES[s] == "TELOS_CORE_IMAGE"} - {"telos-core"})
        for service, (context, dockerfile, variable) in br.BUILD_PLAN.items():
            self.assertEqual(os.path.normpath(built[service]["context"]), os.path.normpath(context))
            self.assertEqual(built[service]["dockerfile"], dockerfile)
            self.assertEqual(rc.BUILT_SERVICES[service], variable)

    def test_the_real_repository_has_a_compat_declaration(self):
        document = br.read_compat(ROOT)
        self.assertEqual(document["schemaVersion"], 1)


if __name__ == "__main__":
    unittest.main()
