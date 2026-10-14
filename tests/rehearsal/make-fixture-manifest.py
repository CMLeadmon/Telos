#!/usr/bin/env python3
"""Build the browser-smoke fixture manifest for a REHEARSAL node.

REHEARSAL ONLY. The smoke needs the Telos item identifiers of one video, one audio
track, one ebook and a chat channel that exist on the node under test. On a
rehearsal node those are the synthetic items tests/rehearsal/make-fixtures.sh
created; this helper signs in as the owner (creating the owner first when the node
is still empty) and records what the node's own catalog calls them.

  make-fixture-manifest.py --public-url URL --out PATH [--bootstrap] < credentials.json

stdin: {"owner":{"username","password"},"bootstrapToken":"..."}; nothing secret is
printed or put in argv.
"""
import argparse
import http.client
import json
import ssl
import sys
import time
import urllib.parse


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--public-url", required=True)
    parser.add_argument("--out", required=True)
    parser.add_argument("--bootstrap", action="store_true")
    args = parser.parse_args()
    credentials = json.loads(sys.stdin.read(65536))
    owner = credentials["owner"]
    parts = urllib.parse.urlsplit(args.public_url)
    context = ssl.create_default_context()  # platform trust only
    cookies = {}

    def call(method, path, body=None, expect=(200, 201)):
        connection = http.client.HTTPSConnection(parts.hostname, parts.port or 443, timeout=30, context=context)
        headers = {"Accept": "application/json", "Origin": f"{parts.scheme}://{parts.netloc}"}
        if cookies:
            headers["Cookie"] = "; ".join(f"{k}={v}" for k, v in cookies.items())
        data = None
        if body is not None:
            data = json.dumps(body).encode()
            headers["Content-Type"] = "application/json"
        connection.request(method, path, body=data, headers=headers)
        response = connection.getresponse()
        raw = response.read()
        for name, value in response.getheaders():
            if name.lower() == "set-cookie":
                pair = value.split(";", 1)[0]
                key, _, cookie = pair.partition("=")
                cookies[key] = cookie
        if response.status not in expect:
            raise SystemExit(f"{method} {path.split('?')[0]} -> {response.status}")
        return json.loads(raw) if raw[:1] in (b"{", b"[") else raw

    if args.bootstrap:
        call("POST", "/api/v1/auth/bootstrap", {"username": owner["username"], "password": owner["password"], "token": credentials["bootstrapToken"]})
    call("POST", "/api/v1/auth/login", {"username": owner["username"], "password": owner["password"]})

    def first(items, *names):
        for item in items:
            label = str(item.get("name") or item.get("Name") or item.get("title") or item.get("Title") or "")
            if any(n.lower() in label.lower() for n in names):
                return item
        return None

    def identifier(item):
        return str(item.get("id") or item.get("ID") or item.get("Id"))

    def leaf(library_id, wanted):
        """Walk a library's folders (artist -> album -> track) to the item named `wanted`."""
        def walk(parent_param, parent, depth):
            for item in call("GET", f"/api/v1/media/items?{parent_param}={urllib.parse.quote(parent)}") or []:
                label = str(item.get("title") or item.get("name") or item.get("Name") or "")
                if wanted.lower() in label.lower() and not item.get("isFolder") and not item.get("folder"):
                    return item
                if depth < 3 and (item.get("isFolder") or item.get("folder") or item.get("type") in ("folder", "MusicArtist", "MusicAlbum")):
                    found = walk("parentId", identifier(item), depth + 1)
                    if found:
                        return found
            return None
        return walk("libraryId", library_id, 0)

    # The catalog is reconciled in the background after the first start.
    deadline = time.time() + 240
    video = audio = book = None
    while time.time() < deadline:
        libraries = call("GET", "/api/v1/media") or []
        books = call("GET", "/api/v1/library/books") or []
        for library in libraries:
            kind = str(library.get("collectionType") or library.get("type") or "")
            if kind in ("movies", "video") and not video:
                video = leaf(identifier(library), "Telos Test Film")
            if kind in ("music", "audio") and not audio:
                audio = leaf(identifier(library), "Test Tone")
        book = first(books, "Telos Test Book")
        if video and audio and book:
            break
        time.sleep(5)
    if not (video and audio and book):
        print(json.dumps({"video": bool(video), "audio": bool(audio), "book": bool(book)}), file=sys.stderr)
        raise SystemExit("a rehearsal fixture is missing from the catalog: Jellyfin or Grimmory has not indexed it")
    channels = call("GET", "/api/v1/channels")
    channel = next((c for c in channels if (c.get("name") or c.get("Name")) == "general"), channels[0])
    manifest = {
        "schemaVersion": 1,
        "chatChannel": channel.get("name") or channel.get("Name"),
        "video": {"id": identifier(video)},
        "audio": {"id": identifier(audio)},
        "ebook": {"id": identifier(book), "format": "epub"},
        # Uploads are limited to document, image, audio and video types and sniffed by
        # content, so the text fixture is a minimal PDF header rather than a .txt.
        "upload": {"filename": "rehearsal-note.pdf", "text": "%PDF-1.4\n%Telos rehearsal upload\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF\n"},
    }
    with open(args.out, "w", encoding="utf-8") as handle:
        json.dump(manifest, handle, indent=1)
    print(f"make-fixture-manifest: wrote {args.out} (video {manifest['video']['id'][:8]}…, audio {manifest['audio']['id'][:8]}…, ebook {manifest['ebook']['id'][:8]}…)")


if __name__ == "__main__":
    main()
