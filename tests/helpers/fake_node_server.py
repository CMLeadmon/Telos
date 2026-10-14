#!/usr/bin/env python3
"""HTTPS fixture node for exercising scripts/lib/browser_node_smoke.py.

NOT a Telos implementation: it speaks just enough of the client/gateway protocol
for the smoke journey and can be told to misbehave (FAULTS) so every assertion has
a failing counterpart. It proves the smoke command's protocol and error handling
only; it can never stand in for a real node or contribute release evidence.

    fake_node_server.py CERT KEY PORT_FILE [--faults a,b] [--stage-fifo BASE]

Prints nothing; writes the bound port to PORT_FILE once listening. With
--stage-fifo it is also the outage controller: it reads "STAGE name" lines from
BASE.out, flips the matching upstream state and answers "GO" on BASE.in.
"""
import base64
import io
import json
import os
import re
import ssl
import sys
import threading
import uuid
import zipfile
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

BOOTSTRAP_TOKEN = "fixture-bootstrap-token"
CHANNEL = {"id": "c-1", "name": "general"}
HLS = b"#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXTINF:2.0,\nsegment0.ts\n#EXT-X-ENDLIST\n"
SEGMENT = b"\x47" + b"\0" * 187


def epub_bytes(valid=True):
    buffer = io.BytesIO()
    with zipfile.ZipFile(buffer, "w") as archive:
        archive.writestr("mimetype", "application/epub+zip" if valid else "text/plain")
    return buffer.getvalue()


class State:
    def __init__(self, faults, flag_dir=None):
        self.flag_dir = flag_dir
        self.faults = set(faults)
        self.lock = threading.Lock()
        self.users = {}          # username -> password
        self.roles = {}
        self.sessions = {}       # cookie -> username
        self.invites = set()
        self.bootstrapped = False
        self.messages = []
        self.files = {}
        self.comments = {}
        self.jellyfin_down = False
        self.grimmory_down = False


class Handler(BaseHTTPRequestHandler):
    state = None
    protocol_version = "HTTP/1.1"

    def log_message(self, *args):
        pass

    # ------------------------------------------------------------------ helpers
    def send(self, status, body=b"", content_type="application/json", headers=None):
        if isinstance(body, (dict, list)):
            body = json.dumps(body).encode()
        elif isinstance(body, str):
            body = body.encode()
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        for key, value in (headers or {}).items():
            self.send_header(key, value)
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(body)

    def read_body(self):
        length = int(self.headers.get("Content-Length") or 0)
        return self.rfile.read(length) if length else b""

    def user(self):
        cookie = self.headers.get("Cookie", "")
        match = re.search(r"telos_session=([A-Za-z0-9]+)", cookie)
        return self.state.sessions.get(match.group(1)) if match else None

    def require_origin(self):
        origin = self.headers.get("Origin")
        host = self.headers.get("Host")
        if origin != f"https://{host}":
            self.send(403, {"error": "origin"})
            return False
        return True

    def issue_session(self, username):
        token = uuid.uuid4().hex
        self.state.sessions[token] = username
        return {"Set-Cookie": f"telos_session={token}; Path=/; HttpOnly; Secure; SameSite=Strict"}

    # ------------------------------------------------------------------ routing
    def do_GET(self):
        self.route()

    def do_POST(self):
        self.route()

    def do_DELETE(self):
        self.route()

    def route(self):
        path = self.path.split("?", 1)[0]
        state = self.state
        if state.flag_dir:
            # An external controller (the rehearsal test's podman stub) drives outages by
            # creating or removing marker files.
            state.jellyfin_down = os.path.exists(os.path.join(state.flag_dir, "jellyfin-down"))
            state.grimmory_down = os.path.exists(os.path.join(state.flag_dir, "grimmory-down"))
        method = self.command
        faults = state.faults
        if method in ("POST", "DELETE", "PUT", "PATCH") and path.startswith("/api/") and not self.require_origin():
            return
        # ---- static client
        if not path.startswith("/api/"):
            if path == "/healthz":
                return self.send(200, "ok" if "no-client-health" not in faults else "gateway", "text/plain") if "healthz-missing" not in faults else self.send(404, "no")
            if path == "/" or re.fullmatch(r"/(chat|stream|library|files|settings|login)/", path):
                if "api-served-root" in faults and path == "/":
                    return self.send(200, {"status": "ok"})
                if "missing-route" in faults and path == "/files/":
                    return self.send(404, "<html>not found</html>", "text/html")
                return self.send(200, "<!doctype html><html><body>telos</body></html>", "text/html")
            return self.send(404, "<html>not found</html>", "text/html")
        # ---- gateway
        if path == "/api/v1/health/live":
            return self.send(200, {"status": "ok"})
        if path == "/api/v1/auth/bootstrap" and method == "POST":
            body = json.loads(self.read_body() or b"{}")
            if state.bootstrapped or body.get("token") != BOOTSTRAP_TOKEN:
                return self.send(403, {"error": "bootstrap"})
            state.bootstrapped = True
            state.users[body["username"]] = body["password"]
            state.roles[body["username"]] = ["Owner"]
            return self.send(201, {"ok": True})
        if path == "/api/v1/auth/login" and method == "POST":
            body = json.loads(self.read_body() or b"{}")
            if state.users.get(body.get("username")) != body.get("password") or not body.get("password"):
                return self.send(401, {"error": "credentials"})
            return self.send(200, {"ok": True}, headers=self.issue_session(body["username"]))
        if path == "/api/v1/auth/me":
            username = self.user()
            if not username:
                return self.send(401, {"error": "auth"})
            roles = state.roles.get(username, ["Member"])
            return self.send(200, {"ID": username, "Username": username.lower(), "Roles": roles})
        if path == "/api/v1/auth/invites" and method == "POST":
            username = self.user()
            if "Owner" not in state.roles.get(username, []):
                return self.send(403, {"error": "forbidden"})
            self.read_body()
            token = uuid.uuid4().hex
            state.invites.add(token)
            return self.send(201, {"invite_token": token, "role_id": "Member"})
        if path == "/api/v1/auth/invites/accept" and method == "POST":
            body = json.loads(self.read_body() or b"{}")
            if body.get("token") not in state.invites:
                return self.send(400, {"error": "invite"})
            state.invites.discard(body["token"])
            state.users[body["username"]] = body["password"]
            return self.send(201, {"ok": True}, headers=self.issue_session(body["username"]))
        if not re.match(r"/api/v1/(channels|media|stream|hls|library|files)(/|$)", path):
            # Like the real mux, a path no route owns is a 404 for everyone, before auth.
            return self.send(404, {"error": "not found"} if "unknown-api-html" not in faults else "<html>fallback</html>",
                             "application/json" if "unknown-api-html" not in faults else "text/html")
        username = self.user()
        if not username:
            return self.send(401, {"error": "auth"})
        if path == "/api/v1/channels":
            return self.send(200, [CHANNEL] if "no-channel" not in faults else [])
        if re.fullmatch(r"/api/v1/channels/c-1/messages", path) and method == "POST":
            body = json.loads(self.read_body() or b"{}")
            if "chat-drops-message" not in faults:
                state.messages.append(body["content"])
            return self.send(201, {"id": "m-1"})
        if path == "/api/v1/channels/c-1/roots":
            return self.send(200, [{"content": m} for m in state.messages])
        if path == "/api/v1/media":
            return self.send(200, [{"id": "lib", "name": "Movies", "type": "video", "collectionType": "movies"}])
        if path == "/api/v1/media/items":
            # Like the real gateway: a listing needs the library (or parent) it is for.
            if "libraryId=" not in self.path and "parentId=" not in self.path:
                return self.send(400, {"error": "Missing parentId or libraryId parameter"})
            return self.send(200, [{"id": "item-1", "name": "Fixture"}])
        if re.fullmatch(r"/api/v1/stream/video/[^/]+", path):
            if state.jellyfin_down:
                # Playback needs Jellyfin live, so this is where its outage shows.
                return self.send(503 if "outage-html" not in faults else 200, "<html>down</html>" if "outage-html" in faults else {"error": "jellyfin"},
                                 "text/html" if "outage-html" in faults else "application/json")
            if "no-hls-redirect" in faults:
                return self.send(200, HLS, "application/vnd.apple.mpegurl")
            return self.send(302, b"", headers={"Location": "/api/v1/hls/locatorABC/index.m3u8"})
        if path == "/api/v1/hls/locatorABC/index.m3u8":
            return self.send(200, HLS if "bad-playlist" not in faults else b"<html>", "application/vnd.apple.mpegurl")
        if path == "/api/v1/hls/locatorABC/segment0.ts":
            return self.send(200, SEGMENT, "video/mp2t")
        if re.fullmatch(r"/api/v1/stream/audio/[^/]+", path):
            ok_range = "audio-200" not in faults
            return self.send(206 if ok_range else 200, b"ID3" + b"\0" * 100,
                             "audio/mpeg" if "audio-wrong-type" not in faults else "text/plain",
                             {"Content-Range": "bytes 0-102/4096"} if ok_range else None)
        if path == "/api/v1/library/books":
            # The listing comes from Telos's own catalog, so it survives an outage.
            return self.send(200, [{"id": "book-1", "title": "Fixture"}])
        if re.fullmatch(r"/api/v1/library/books/[^/]+/content", path):
            if state.grimmory_down:
                return self.send(502, {"error": "grimmory"})
            return self.send(200, epub_bytes("bad-epub" not in faults), "application/epub+zip")
        if path == "/api/v1/files" and method == "POST":
            raw = self.read_body()
            match = re.search(rb'filename="([^"]+)"\r\nContent-Type: [^\r]+\r\n\r\n(.*)\r\n--', raw, re.S)
            file_id = uuid.uuid4().hex[:8]
            data = match.group(2)
            state.files[file_id] = data if "download-mismatch" not in faults else data + b"!"
            return self.send(201, {"id": file_id})
        if path == "/api/v1/files":
            return self.send(200, [{"id": k} for k in state.files])
        match = re.fullmatch(r"/api/v1/files/([0-9a-f]+)/(download|comments)", path)
        if match:
            file_id, what = match.groups()
            if file_id not in state.files:
                return self.send(404, {"error": "missing"})
            if what == "download":
                return self.send(200, state.files[file_id], "application/octet-stream")
            if method == "POST":
                body = json.loads(self.read_body() or b"{}")
                state.comments.setdefault(file_id, []).append(body["note"])
                return self.send(201, {"ok": True})
            return self.send(200, {"annotations": [{"note": c} for c in state.comments.get(file_id, [])]} if "comment-lost" not in faults else {"annotations": []})
        match = re.fullmatch(r"/api/v1/files/([0-9a-f]+)", path)
        if match and method == "DELETE":
            if "delete-ignored" not in faults:
                state.files.pop(match.group(1), None)
            return self.send(204, b"")
        return self.send(404, {"error": "not found"} if "unknown-api-html" not in faults else "<html>fallback</html>",
                         "application/json" if "unknown-api-html" not in faults else "text/html")


def stage_controller(base, state):
    out_path, in_path = base + ".out", base + ".in"
    while True:
        with open(out_path, "r", encoding="utf-8") as pipe:
            line = pipe.readline().strip()
        if not line:
            continue
        name = line.split(" ", 1)[1]
        if name == "jellyfin-down":
            state.jellyfin_down = True
            if "outage-breaks-chat" in state.faults:
                state.sessions.clear()
        elif name == "grimmory-down":
            state.jellyfin_down = False
            state.grimmory_down = True
        elif name == "restored":
            state.jellyfin_down = state.grimmory_down = False if "never-recovers" not in state.faults else state.grimmory_down
        with open(in_path, "w", encoding="utf-8") as pipe:
            pipe.write("GO\n")


def main():
    certificate, key, port_file = sys.argv[1:4]
    faults = []
    stage = None
    flag_dir = None
    arguments = sys.argv[4:]
    while arguments:
        flag = arguments.pop(0)
        if flag == "--faults":
            faults = [f for f in arguments.pop(0).split(",") if f]
        elif flag == "--stage-fifo":
            stage = arguments.pop(0)
        elif flag == "--flag-dir":
            flag_dir = arguments.pop(0)
    Handler.state = State(faults, flag_dir)
    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    context.load_cert_chain(certificate, key)
    server.socket = context.wrap_socket(server.socket, server_side=True)
    if stage:
        threading.Thread(target=stage_controller, args=(stage, Handler.state), daemon=True).start()
    with open(port_file, "w", encoding="utf-8") as handle:
        handle.write(str(server.server_port))
    server.serve_forever()


if __name__ == "__main__":
    main()
