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

    # The catalog is reconciled in the background after the first start.
    deadline = time.time() + 240
    media = books = None
    while time.time() < deadline:
        media = call("GET", "/api/v1/media/items")
        books = call("GET", "/api/v1/library/books")
        if media and books:
            break
        time.sleep(5)
    if not media or not books:
        raise SystemExit("the catalog stayed empty: Jellyfin or Grimmory has not been indexed")
    video = first(media, "Telos Test Film")
    audio = first(media, "Test Tone")
    book = first(books, "Telos Test Book")
    if not (video and audio and book):
        print(json.dumps({"media": [str(m.get("name") or m.get("Name")) + ":" + str(m.get("type") or m.get("Type")) for m in media][:20],
                          "books": [str(b.get("title") or b.get("name")) for b in books][:20]}), file=sys.stderr)
        raise SystemExit("a rehearsal fixture is missing from the catalog")
    channels = call("GET", "/api/v1/channels")
    channel = channels[0]
    manifest = {
        "schemaVersion": 1,
        "chatChannel": channel.get("name") or channel.get("Name"),
        "video": {"id": identifier(video)},
        "audio": {"id": identifier(audio)},
        "ebook": {"id": identifier(book), "format": "epub"},
        "upload": {"filename": "rehearsal-note.txt", "text": "Telos rehearsal upload.\n"},
    }
    with open(args.out, "w", encoding="utf-8") as handle:
        json.dump(manifest, handle, indent=1)
    print(f"make-fixture-manifest: wrote {args.out} (video {manifest['video']['id'][:8]}…, audio {manifest['audio']['id'][:8]}…, ebook {manifest['ebook']['id'][:8]}…)")


if __name__ == "__main__":
    main()
