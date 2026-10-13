#!/usr/bin/env python3
"""Cookie-mode, same-origin member journey against a Telos browser node.

One bounded, argv-safe probe of the assembled node over platform-trusted HTTPS:
the static client answers the browser routes, the gateway answers /api/v1, an
owner and an invited member can sign in, chat, store and comment on a file, read
real media and a real ebook, and a Jellyfin or Grimmory outage degrades only its
own module. It writes the shared gate-result record (recordKind rehearsal or
candidate) plus a canonical report; it never writes an evidence envelope.

Credentials arrive as one JSON object on stdin and are never printed, logged or
placed in argv/environment. Cookies live in a 0600 jar under a 0700 temporary
directory that is removed on every exit.
"""

import argparse
import base64
import http.client
import io
import json
import os
import platform
import re
import shutil
import ssl
import stat
import sys
import tempfile
import time
import urllib.parse
import uuid
import zipfile
from pathlib import Path

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import release_graph as rg  # noqa: E402

EXIT_FAILED = 1
EXIT_USAGE = 2
EXIT_NOT_RUN = 3
REQUEST_TIMEOUT = 20
TOTAL_BUDGET_SECONDS = 600
MAX_BODY = 8 * 1024 * 1024
STAGES = ("jellyfin-down", "grimmory-down", "restored")


class SmokeError(Exception):
    """A usage or environment problem (not an assertion failure)."""


class Assertion(Exception):
    """One journey assertion did not hold."""


def fail_usage(message):
    raise SmokeError(message)


# --------------------------------------------------------------------------- HTTP

class Session:
    """A minimal HTTPS client: no redirects followed, cookies in a private jar."""

    def __init__(self, origin, context, jar_path):
        parsed = urllib.parse.urlsplit(origin)
        self.origin = f"{parsed.scheme}://{parsed.netloc}"
        self.host = parsed.hostname
        self.port = parsed.port or 443
        self.context = context
        self.jar_path = jar_path
        self.cookies = {}
        self.deadline = time.monotonic() + TOTAL_BUDGET_SECONDS

    def _store(self, headers):
        for name, value in headers:
            if name.lower() == "set-cookie":
                pair = value.split(";", 1)[0]
                if "=" in pair:
                    key, _, cookie = pair.partition("=")
                    attributes = value.lower()
                    if "max-age=0" in attributes or cookie == "":
                        self.cookies.pop(key.strip(), None)
                    else:
                        self.cookies[key.strip()] = cookie
        descriptor = os.open(self.jar_path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(descriptor, "w", encoding="utf-8") as handle:
            json.dump(self.cookies, handle)

    def request(self, method, path, headers=None, body=None, json_body=None, timeout=REQUEST_TIMEOUT):
        if time.monotonic() > self.deadline:
            raise Assertion("the journey exceeded its overall time budget")
        send = {"Accept": "*/*", "User-Agent": "telos-smoke/1"}
        send.update(headers or {})
        if json_body is not None:
            body = json.dumps(json_body).encode("utf-8")
            send["Content-Type"] = "application/json"
        if method not in ("GET", "HEAD", "OPTIONS"):
            send["Origin"] = self.origin
        if self.cookies:
            send["Cookie"] = "; ".join(f"{k}={v}" for k, v in self.cookies.items())
        connection = http.client.HTTPSConnection(self.host, self.port, timeout=timeout, context=self.context)
        try:
            connection.request(method, path, body=body, headers=send)
            response = connection.getresponse()
            data = response.read(MAX_BODY + 1)
            if len(data) > MAX_BODY:
                raise Assertion(f"{method} {path.split('?')[0]} returned more than {MAX_BODY} bytes")
            header_list = response.getheaders()
            self._store(header_list)
            return response.status, {k.lower(): v for k, v in header_list}, data
        except ssl.SSLError as error:
            raise Assertion(f"TLS verification failed: {type(error).__name__}") from error
        except (OSError, http.client.HTTPException) as error:
            raise Assertion(f"{method} {path.split('?')[0]} failed: {type(error).__name__}") from error
        finally:
            connection.close()

    def json(self, method, path, expected=(200,), **kwargs):
        status, headers, data = self.request(method, path, **kwargs)
        if status not in expected:
            raise Assertion(f"{method} {path.split('?')[0]} returned {status}, expected {sorted(expected)}")
        if "json" not in headers.get("content-type", ""):
            raise Assertion(f"{method} {path.split('?')[0]} did not answer JSON")
        try:
            return json.loads(data) if data else None
        except ValueError as error:
            raise Assertion(f"{method} {path.split('?')[0]} returned malformed JSON") from error


# --------------------------------------------------------------------------- journey

class Journey:
    def __init__(self, session, manifest, credentials, bootstrap, stage_pipe):
        self.anonymous = session
        self.manifest = manifest
        self.credentials = credentials
        self.bootstrap = bootstrap
        self.stage_pipe = stage_pipe
        self.report = []
        self.owner = None
        self.member = None
        self.uploaded = None

    def check(self, identifier, description, function, required=True):
        try:
            detail = function()
            self.report.append({"id": identifier, "description": description, "status": "passed", "detail": str(detail or "ok")[:200]})
            return True
        except Assertion as error:
            self.report.append({"id": identifier, "description": description, "status": "failed", "detail": str(error)[:300]})
        except (SmokeError, KeyError, TypeError, ValueError) as error:
            self.report.append({"id": identifier, "description": description, "status": "failed",
                                "detail": f"unexpected {type(error).__name__}"})
        return not required

    def clone(self):
        copy = Session(self.anonymous.origin, self.anonymous.context, self.anonymous.jar_path + "." + uuid.uuid4().hex[:8])
        return copy

    # ---- assertions
    def edge(self):
        def root():
            status, headers, data = self.anonymous.request("GET", "/")
            if status != 200 or "text/html" not in headers.get("content-type", "") or b"<html" not in data.lower():
                raise Assertion("the root path did not serve the exported client HTML")
            return "client HTML"

        def client_health():
            status, _, data = self.anonymous.request("GET", "/healthz")
            if status != 200 or data.strip() != b"ok":
                raise Assertion("/healthz is not the client container's own health answer")
            return "client /healthz ok"

        def api_live():
            body = self.anonymous.json("GET", "/api/v1/health/live")
            return json.dumps(body)[:80]

        def unknown_api():
            status, headers, _ = self.anonymous.request("GET", "/api/v1/definitely-not-a-route")
            if "text/html" in headers.get("content-type", ""):
                raise Assertion("an unknown API path was answered with HTML (client fallback)")
            if status != 404:
                raise Assertion(f"an unknown API path answered {status}, expected the gateway's 404")
            return "gateway 404"

        def module_routes():
            for route in ("/chat/", "/stream/", "/library/", "/files/", "/settings/", "/login/"):
                status, headers, _ = self.anonymous.request("GET", route)
                if status != 200 or "text/html" not in headers.get("content-type", ""):
                    raise Assertion(f"{route} did not resolve from the client")
            return "six routes"

        self.check("edge.client-root", "root HTML comes from telos-client", root)
        self.check("edge.client-health", "client health is independent of the gateway", client_health)
        self.check("edge.api-live", "/api/v1/health/live comes from telos-core", api_live)
        self.check("edge.unknown-api", "unknown API paths are gateway 404s, never client HTML", unknown_api)
        self.check("edge.module-routes", "every module route resolves from the client", module_routes)

    def sign_in(self):
        owner = self.credentials["owner"]
        member = self.credentials["member"]
        if self.bootstrap:
            def bootstrap():
                self.anonymous.json("POST", "/api/v1/auth/bootstrap", expected=(200, 201),
                                    json_body={"username": owner["username"], "password": owner["password"],
                                               "token": self.credentials["bootstrapToken"]})
                return "owner created"
            self.check("auth.bootstrap", "empty node accepts the owner bootstrap", bootstrap)

        self.owner = self.clone()

        def owner_login():
            self.owner.json("POST", "/api/v1/auth/login", expected=(200,),
                            json_body={"username": owner["username"], "password": owner["password"]})
            me = self.owner.json("GET", "/api/v1/auth/me")
            if "Owner" not in (me.get("Roles") or me.get("roles") or []):
                raise Assertion("the owner session does not carry the Owner role")
            return "owner session"
        self.check("auth.owner-login", "the owner signs in over HTTPS", owner_login)

        invite = {}

        def create_invite():
            created = self.owner.json("POST", "/api/v1/auth/invites", expected=(200, 201), json_body={"roleId": "Member"})
            token = created.get("token") or created.get("Token")
            if not token:
                raise Assertion("the invite response carried no token")
            invite["token"] = token
            return "invite created"
        self.check("auth.invite", "the owner creates an invite", create_invite)

        self.member = self.clone()

        def accept():
            self.member.json("POST", "/api/v1/auth/invites/accept", expected=(200, 201),
                             json_body={"token": invite["token"], "username": member["username"], "password": member["password"]})
            me = self.member.json("GET", "/api/v1/auth/me")
            if (me.get("Username") or me.get("username")) != member["username"].lower():
                raise Assertion("the member session is not for the invited account")
            return "member joined"
        self.check("auth.accept", "an invited member joins and holds a session", accept)

    def chat(self):
        marker = f"smoke-{uuid.uuid4().hex[:10]}"

        def converse():
            channels = self.member.json("GET", "/api/v1/channels")
            channel = next((c for c in channels if (c.get("name") or c.get("Name")) == self.manifest["chatChannel"]), None)
            if channel is None:
                raise Assertion("the fixture chat channel is not visible to the member")
            channel_id = channel.get("id") or channel.get("ID")
            self.member.json("POST", f"/api/v1/channels/{channel_id}/messages", expected=(200, 201), json_body={"content": marker})
            roots = self.member.json("GET", f"/api/v1/channels/{channel_id}/roots")
            if marker not in json.dumps(roots):
                raise Assertion("the posted message is not in the channel history")
            return "message round trip"
        self.check("chat.round-trip", "a member posts and reads a Chat message", converse)

    def stream(self):
        video = self.manifest["video"]["id"]
        audio = self.manifest["audio"]["id"]

        def hls():
            media = self.member.json("GET", "/api/v1/media/items")
            if not media:
                raise Assertion("the Stream catalog is empty")
            status, headers, _ = self.member.request("GET", f"/api/v1/stream/video/{urllib.parse.quote(video, safe='')}")
            if status not in (302, 303, 307):
                raise Assertion(f"video entry returned {status}, expected an HLS redirect")
            location = urllib.parse.urlsplit(headers.get("location", ""))
            path = location.path + (f"?{location.query}" if location.query else "")
            status, _, playlist = self.member.request("GET", path)
            if status != 200 or not playlist.startswith(b"#EXTM3U"):
                raise Assertion("the HLS playlist is not a valid M3U8")
            segments = [line for line in playlist.decode("utf-8", "replace").splitlines() if line and not line.startswith("#")]
            if not segments:
                raise Assertion("the HLS playlist lists no media")
            base = path.rsplit("/", 1)[0] + "/"
            target = segments[0] if segments[0].startswith("/") else base + segments[0]
            status, _, segment = self.member.request("GET", target)
            if status != 200 or not segment:
                raise Assertion("the first HLS segment could not be fetched")
            return f"playlist + {len(segment)} byte segment"
        self.check("stream.video", "an HLS playlist and its first segment play from the node", hls)

        def audio_range():
            status, headers, body = self.member.request("GET", f"/api/v1/stream/audio/{urllib.parse.quote(audio, safe='')}", headers={"Range": "bytes=0-1023"})
            if status != 206 or not headers.get("content-range", "").startswith("bytes 0-"):
                raise Assertion(f"a range request for audio answered {status} without a Content-Range; seeking would fail")
            if not headers.get("content-type", "").startswith("audio/") or not body:
                raise Assertion("audio did not stream with an audio content type")
            return headers.get("content-type")
        self.check("stream.audio", "audio streams with range support", audio_range)

    def library(self):
        book = self.manifest["ebook"]

        def read():
            items = self.member.json("GET", "/api/v1/library/books")
            if not items:
                raise Assertion("the Library catalog is empty")
            status, _, data = self.member.request("GET", f"/api/v1/library/books/{urllib.parse.quote(book['id'], safe='')}/content")
            if status != 200 or not data:
                raise Assertion("the fixture ebook content could not be fetched")
            if book["format"] == "epub":
                try:
                    with zipfile.ZipFile(io.BytesIO(data)) as archive:
                        if archive.read("mimetype").strip() != b"application/epub+zip":
                            raise Assertion("the fixture EPUB has the wrong mimetype entry")
                except zipfile.BadZipFile as error:
                    raise Assertion("the fixture EPUB is not a valid archive") from error
            elif book["format"] == "pdf":
                if not data.startswith(b"%PDF-"):
                    raise Assertion("the fixture PDF has no PDF header")
            else:
                raise Assertion("unsupported fixture ebook format")
            return f"{book['format']} {len(data)} bytes"
        self.check("library.read", "a licensed ebook is delivered as a valid document", read)

    def files(self):
        name = f"{uuid.uuid4().hex[:8]}-{self.manifest['upload']['filename']}"
        content = self.manifest["upload"]["text"].encode("utf-8")
        state = {}

        def upload():
            boundary = "----telos" + uuid.uuid4().hex
            body = (f"--{boundary}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"{name}\"\r\n"
                    f"Content-Type: text/plain\r\n\r\n").encode() + content + f"\r\n--{boundary}--\r\n".encode()
            created = self.member.json("POST", "/api/v1/files", expected=(200, 201),
                                       headers={"Content-Type": f"multipart/form-data; boundary={boundary}"}, body=body)
            state["id"] = created.get("id") or created.get("ID")
            if not state["id"]:
                raise Assertion("the upload response carried no file id")
            return "uploaded"
        self.check("files.upload", "a member uploads a scanned file", upload)

        def download():
            status, _, data = self.member.request("GET", f"/api/v1/files/{state['id']}/download")
            if status != 200 or data != content:
                raise Assertion("the downloaded bytes differ from the uploaded bytes")
            return f"{len(data)} bytes identical"
        self.check("files.download", "the file downloads byte-for-byte", download)

        def comment():
            marker = f"note-{uuid.uuid4().hex[:8]}"
            self.member.json("POST", f"/api/v1/files/{state['id']}/comments", expected=(200, 201), json_body={"body": marker, "visibility": "private"})
            listing = self.member.json("GET", f"/api/v1/files/{state['id']}/comments")
            if marker not in json.dumps(listing):
                raise Assertion("the comment is not readable after being written")
            return "comment round trip"
        self.check("commentary.file", "commentary attaches to and reads from a file", comment)

        def delete():
            self.member.request("DELETE", f"/api/v1/files/{state['id']}")
            status, _, _ = self.member.request("GET", f"/api/v1/files/{state['id']}/download")
            if status != 404:
                raise Assertion(f"a deleted file still answers {status}")
            return "deleted"
        self.check("files.delete", "the member deletes the file", delete)

    # ---- outage stages
    def stage(self, name):
        if self.stage_pipe is None:
            return False
        out_path, in_path = self.stage_pipe
        with open(out_path, "w", encoding="utf-8") as pipe:
            pipe.write(f"STAGE {name}\n")
        with open(in_path, "r", encoding="utf-8") as pipe:
            if pipe.readline().strip() != "GO":
                raise Assertion(f"the controller did not acknowledge stage {name}")
        return True

    def outages(self):
        def degraded(stage, module_path, label):
            def probe():
                if not self.stage(stage):
                    raise SmokeError("outage control is unavailable")
                status, headers, _ = self.member.request("GET", module_path)
                if status not in (502, 503, 504):
                    raise Assertion(f"{label} answered {status} during its outage instead of a degraded 502/503")
                if "text/html" in headers.get("content-type", ""):
                    raise Assertion(f"{label} answered HTML during its outage")
                channels = self.member.request("GET", "/api/v1/channels")[0]
                files = self.member.request("GET", "/api/v1/files")[0]
                if channels != 200 or files != 200:
                    raise Assertion(f"Chat ({channels}) or Files ({files}) broke while {label} was down")
                me = self.member.request("GET", "/api/v1/auth/me")[0]
                if me != 200:
                    raise Assertion("authentication broke during the outage")
                return f"{label} degraded; Chat and Files unaffected"
            return probe

        if self.stage_pipe is None:
            for identifier in ("outage.jellyfin", "outage.grimmory", "outage.restored"):
                self.report.append({"id": identifier, "description": "outage stage", "status": "not_run",
                                    "detail": "no outage controller (--stage-pipe) was attached"})
            return
        self.check("outage.jellyfin", "a Jellyfin outage degrades Stream only", degraded("jellyfin-down", "/api/v1/media/items", "Stream"))
        self.check("outage.grimmory", "a Grimmory outage degrades Library only", degraded("grimmory-down", "/api/v1/library/books", "Library"))

        def restored():
            if not self.stage("restored"):
                raise SmokeError("outage control is unavailable")
            if self.member.request("GET", "/api/v1/media/items")[0] != 200 or self.member.request("GET", "/api/v1/library/books")[0] != 200:
                raise Assertion("Stream or Library did not recover")
            return "both modules recovered"
        self.check("outage.restored", "both modules recover after the outages", restored)

    def run(self):
        self.edge()
        self.sign_in()
        if self.member is not None and any(r["id"] == "auth.accept" and r["status"] == "passed" for r in self.report):
            self.chat()
            self.stream()
            self.library()
            self.files()
            self.outages()
        else:
            self.report.append({"id": "journey.blocked", "description": "member journey", "status": "failed",
                                "detail": "sign-in did not complete, so no module could be exercised"})
        return self.report


# --------------------------------------------------------------------------- CLI

def validate_origin(url):
    parsed = urllib.parse.urlsplit(url)
    if parsed.scheme != "https":
        fail_usage("--public-url must be https; plain HTTP is never accepted")
    if not parsed.hostname or parsed.username or parsed.password:
        fail_usage("--public-url must be a bare https origin")
    if re.fullmatch(r"[0-9.]+|\[?[0-9a-fA-F:]+\]?", parsed.hostname):
        fail_usage("--public-url must use a DNS name, not an IP literal")
    if parsed.path not in ("", "/") or parsed.query or parsed.fragment:
        fail_usage("--public-url must not contain a path, query or fragment")
    return f"https://{parsed.netloc}"


def load_manifest(path):
    document = rg.parse_json(Path(path).read_bytes())
    required = {"schemaVersion", "chatChannel", "video", "audio", "ebook", "upload"}
    if not isinstance(document, dict) or set(document) != required or document["schemaVersion"] != 1:
        fail_usage("the fixture manifest must be a closed object with schemaVersion 1")
    for key in ("video", "audio"):
        if not isinstance(document[key], dict) or set(document[key]) != {"id"} or not document[key]["id"]:
            fail_usage(f"fixture {key} must be {{id}}")
    ebook = document["ebook"]
    if not isinstance(ebook, dict) or set(ebook) != {"id", "format"} or ebook["format"] not in ("epub", "pdf") or not ebook["id"]:
        fail_usage("fixture ebook must be {id, format: epub|pdf}")
    upload = document["upload"]
    if not isinstance(upload, dict) or set(upload) != {"filename", "text"} or not upload["filename"] or not upload["text"]:
        fail_usage("fixture upload must be {filename, text}")
    if not isinstance(document["chatChannel"], str) or not document["chatChannel"]:
        fail_usage("fixture chatChannel must be a channel name")
    return document


def read_credentials(bootstrap):
    if sys.stdin.isatty():
        fail_usage("credentials are read from stdin as one JSON object; none was provided")
    try:
        document = json.loads(sys.stdin.read(65536))
    except ValueError:
        fail_usage("stdin is not a JSON credentials object")
    allowed = {"owner", "member", "bootstrapToken"}
    if not isinstance(document, dict) or not set(document) <= allowed or not {"owner", "member"} <= set(document):
        fail_usage("stdin must contain owner and member credentials (and bootstrapToken for an empty node)")
    for role in ("owner", "member"):
        entry = document[role]
        if not isinstance(entry, dict) or set(entry) != {"username", "password"} or not all(isinstance(v, str) and v for v in entry.values()):
            fail_usage(f"{role} credentials must be {{username, password}}")
    if bootstrap and not document.get("bootstrapToken"):
        fail_usage("--bootstrap-empty-node requires bootstrapToken on stdin")
    if not bootstrap and "bootstrapToken" in document:
        fail_usage("a bootstrapToken was supplied without --bootstrap-empty-node")
    return document


def build_outputs(args, report, host_identities, source_commit, lock_digest):
    out = Path(args.result_out).parent
    statuses = [row["status"] for row in report]
    if any(s == "failed" for s in statuses):
        status, code = "failed", EXIT_FAILED
    elif any(s == "not_run" for s in statuses):
        status, code = "not_run", EXIT_NOT_RUN
    else:
        status, code = "passed", 0
    failed = [row["id"] for row in report if row["status"] == "failed"]
    reason = {
        "passed": f"{len(report)} journey assertions passed over verified HTTPS",
        "failed": "failed assertions: " + ", ".join(failed[:8]),
        "not_run": "assertions not run: " + ", ".join(row["id"] for row in report if row["status"] == "not_run"),
    }[status]
    report_doc = {"schemaVersion": 1, "origin": args.public_url, "assertions": report}
    report_bytes = rg.canonical_bytes(report_doc)
    (out / "report.json").write_bytes(report_bytes)
    attachments = [{"path": "report.json", "sha256": rg.raw_sha256(report_bytes), "kind": "report"}]
    record = {
        "schemaVersion": 1, "recordKind": "candidate" if args.candidate_lock else "rehearsal",
        "gateId": os.environ.get("TELOS_GATE_ID", "clean-install"), "sourceCommit": source_commit,
        "candidateLockDigest": lock_digest, "status": status, "reason": reason, "subjectFile": None,
        "attachments": attachments, "toolVersions": {"python3": platform.python_version()},
    }
    if status == "passed":
        descriptor = {
            "schemaVersion": 1, "kind": "external-host", "sourceCommit": source_commit,
            "candidateLockDigest": lock_digest, "observedAt": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            "purpose": record["gateId"], "hosts": host_identities,
            "records": [{"path": "report.json", "sha256": attachments[0]["sha256"]}],
        }
        descriptor_bytes = rg.canonical_bytes(descriptor)
        (out / "subject.json").write_bytes(descriptor_bytes)
        attachments.append({"path": "subject.json", "sha256": rg.raw_sha256(descriptor_bytes), "kind": "subject"})
        attachments.sort(key=lambda item: item["path"])
        record["subjectFile"] = "subject.json"
    rg.validate_record("gate-result", record)
    rg.write_atomic_exclusive(args.result_out, rg.canonical_bytes(record))
    return status, code, reason


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--public-url", required=True)
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--rehearsal", action="store_true")
    mode.add_argument("--candidate-lock")
    parser.add_argument("--source-commit")
    parser.add_argument("--rehearsal-lock")
    parser.add_argument("--result-out", required=True)
    parser.add_argument("--fixture-manifest", required=True)
    parser.add_argument("--bootstrap-empty-node", action="store_true")
    parser.add_argument("--stage-pipe", help="base path of the FIFO pair BASE.out / BASE.in (outage controller)")
    parser.add_argument("--host-identity", help="sanitized JSON array of observed node host descriptors")
    args = parser.parse_args(argv)

    temp = Path(tempfile.mkdtemp(prefix="telos-smoke."))
    os.chmod(temp, 0o700)
    try:
        args.public_url = validate_origin(args.public_url)
        manifest = load_manifest(args.fixture_manifest)
        credentials = read_credentials(args.bootstrap_empty_node)
        if args.rehearsal:
            if not args.source_commit or not re.fullmatch(r"[0-9a-f]{40}", args.source_commit):
                fail_usage("--rehearsal requires --source-commit with a 40-hex commit")
            source_commit, lock_digest = args.source_commit, None
            if args.rehearsal_lock:
                lock_bytes = Path(args.rehearsal_lock).read_bytes()
                source_commit = json.loads(lock_bytes)["sourceCommit"]
                lock_digest = rg.digest_bytes(lock_bytes)
        else:
            if args.source_commit or args.rehearsal_lock:
                fail_usage("--source-commit and --rehearsal-lock belong to --rehearsal only")
            lock_bytes = Path(args.candidate_lock).read_bytes()
            source_commit, lock_digest = json.loads(lock_bytes)["sourceCommit"], rg.digest_bytes(lock_bytes)
        if not Path(args.result_out).parent.is_dir():
            fail_usage("the --result-out directory must exist")
        host_identities = []
        if args.host_identity:
            host_identities = json.loads(Path(args.host_identity).read_text())
            rg.validate_record("observed-subject", {
                "schemaVersion": 1, "kind": "external-host", "sourceCommit": source_commit, "candidateLockDigest": lock_digest,
                "observedAt": "2026-01-01T00:00:00Z", "purpose": "clean-install", "hosts": host_identities, "records": []})
        stage_pipe = None
        if args.stage_pipe:
            stage_pipe = (args.stage_pipe + ".out", args.stage_pipe + ".in")
            for fifo in stage_pipe:
                if not stat.S_ISFIFO(os.stat(fifo).st_mode):
                    fail_usage(f"{fifo} is not a FIFO")

        # TLS verification stays on: the platform trust store (or SSL_CERT_FILE) decides.
        context = ssl.create_default_context()
        session = Session(args.public_url, context, str(temp / "anonymous.jar"))
        journey = Journey(session, manifest, credentials, args.bootstrap_empty_node, stage_pipe)
        report = journey.run()
        if not host_identities and not args.rehearsal:
            report.append({"id": "identity.host", "description": "observed node host identity", "status": "not_run",
                           "detail": "no --host-identity descriptor was supplied by the drill"})
        elif not host_identities:
            host_identities = [{"hostId": "node-under-rehearsal", "identityMethod": "tester-assigned",
                                "os": "unknown", "osVersion": "unknown", "arch": "unknown"}]
        status, code, reason = build_outputs(args, report, host_identities, source_commit, lock_digest)
        print(f"smoke-browser-node: {status}: {reason}")
        return code
    except SmokeError as error:
        print(f"smoke-browser-node: {error}", file=sys.stderr)
        return EXIT_USAGE
    except (OSError, ValueError, KeyError, rg.GraphError) as error:
        print(f"smoke-browser-node: {type(error).__name__}: {str(error)[:200]}", file=sys.stderr)
        return EXIT_USAGE
    finally:
        shutil.rmtree(temp, ignore_errors=True)


if __name__ == "__main__":
    sys.exit(main())
