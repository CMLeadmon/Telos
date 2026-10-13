#!/usr/bin/env python3
"""Automate the manual first-run steps of `telos configure integrations --setup`.

REHEARSAL ONLY. The product asks a human to finish Jellyfin's wizard, add libraries
and create an API key, and to create Grimmory's gateway account and libraries. On a
reference VM nobody is there to click, so this drives the same public HTTP APIs the
web UIs use, over the loopback ports the setup overlay publishes:

  provision-upstreams.py --node-env PATH --out DIR

It creates the Jellyfin admin ("rehearsal"), Movies and Music libraries, and an API
key; and Grimmory's gateway admin (password from node.env, exactly the account the
guided command expects), plus one library over /books. The Jellyfin API key goes to
DIR/jellyfin-key (mode 0600) for the PTY driver to type at the hidden prompt. It is a
fixture generator, not a product path, and prints no secret.
"""
import argparse
import json
import os
import re
import sys
import time
import urllib.error
import urllib.request

JELLYFIN = "http://127.0.0.1:8096"
GRIMMORY = "http://127.0.0.1:6060"
AUTH = 'MediaBrowser Client="telos-rehearsal", Device="vm", DeviceId="telos-rehearsal", Version="1.0.0"'


def call(url, method="GET", body=None, headers=None, expect=(200, 204), timeout=60):
    data = None if body is None else json.dumps(body).encode()
    request = urllib.request.Request(url, data=data, method=method, headers={"Content-Type": "application/json", **(headers or {})})
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            raw = response.read()
            status = response.status
    except urllib.error.HTTPError as error:
        raw, status = error.read(), error.code
    if status not in expect:
        raise SystemExit(f"{method} {url} -> {status}: {raw[:200]!r}")
    return json.loads(raw) if raw and raw.strip()[:1] in (b"{", b"[") else raw


def wait(url, label, seconds=240):
    deadline = time.time() + seconds
    while time.time() < deadline:
        try:
            urllib.request.urlopen(url, timeout=5).read()
            return
        except Exception:
            time.sleep(2)
    raise SystemExit(f"{label} did not become reachable")


def jellyfin_session(password):
    reply = call(f"{JELLYFIN}/Users/AuthenticateByName", "POST", {"Username": "rehearsal", "Pw": password}, {"X-Emby-Authorization": AUTH})
    return {"X-Emby-Authorization": AUTH + f', Token="{reply["AccessToken"]}"'}


def provision_grimmory(args, env):
    wait(f"{GRIMMORY}/api/v1/healthcheck", "Grimmory")
    user = env.get("GRIMMORY_ADMIN_USER") or "telos-gateway"
    gp = env["GRIMMORY_ADMIN_PASSWORD"]
    # Idempotent: a gateway account or library from an earlier attempt is reused.
    call(f"{GRIMMORY}/api/v1/setup", "POST", {"username": user, "name": "Telos Gateway", "email": "gateway@telos.test", "password": gp}, expect=(200, 400, 403, 409))
    token = call(f"{GRIMMORY}/api/v1/auth/login", "POST", {"username": user, "password": gp})["accessToken"]
    bearer = {"Authorization": f"Bearer {token}"}
    if not call(f"{GRIMMORY}/api/v1/libraries", "GET", None, bearer):
        call(f"{GRIMMORY}/api/v1/libraries", "POST", {"name": "Telos Books", "icon": "book", "watch": True, "paths": [{"path": "/books"}], "scanMode": "FILE_AS_BOOK"}, bearer)
    print("provision-upstreams: Jellyfin (Movies, Music, API key) and Grimmory (gateway admin, Telos Books) are ready")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--node-env", required=True)
    parser.add_argument("--out", required=True)
    args = parser.parse_args()
    os.makedirs(args.out, exist_ok=True)
    os.chmod(args.out, 0o700)
    env = {}
    for line in open(args.node_env, encoding="utf-8"):
        match = re.match(r"([A-Z][A-Z0-9_]*)=(.*)$", line.rstrip("\n"))
        if match:
            env[match.group(1)] = match.group(2).strip("'")
    password = "rehearsal-" + os.urandom(8).hex()

    wait(f"{JELLYFIN}/System/Info/Public", "Jellyfin")
    info = call(f"{JELLYFIN}/System/Info/Public")
    key_path = os.path.join(args.out, "jellyfin-key")
    if info.get("StartupWizardCompleted"):
        # Idempotent: a server this helper already provisioned keeps its API key file.
        if not os.path.exists(key_path):
            raise SystemExit("Jellyfin is already configured and no API key file from an earlier run exists; wipe the jellyfin_config volume")
        provision_grimmory(args, env)
        return 0
    call(f"{JELLYFIN}/Startup/Configuration", "POST", {"UICulture": "en-US", "MetadataCountryCode": "US", "PreferredMetadataLanguage": "en"})
    call(f"{JELLYFIN}/Startup/User")
    call(f"{JELLYFIN}/Startup/User", "POST", {"Name": "rehearsal", "Password": password})
    call(f"{JELLYFIN}/Startup/RemoteAccess", "POST", {"EnableRemoteAccess": False, "EnableAutomaticPortMapping": False})
    call(f"{JELLYFIN}/Startup/Complete", "POST")
    session = jellyfin_session(password)
    for name, kind, path in (("Movies", "movies", "/data/media/movies"), ("Music", "music", "/data/media/music")):
        call(f"{JELLYFIN}/Library/VirtualFolders?name={name}&collectionType={kind}&refreshLibrary=true", "POST",
             {"LibraryOptions": {"PathInfos": [{"Path": path}], "EnableRealtimeMonitor": False}}, session)
    call(f"{JELLYFIN}/Auth/Keys?app=telos", "POST", None, session)
    keys = call(f"{JELLYFIN}/Auth/Keys", "GET", None, session)
    key = next(item["AccessToken"] for item in keys["Items"] if item["AppName"] == "telos")
    path = os.path.join(args.out, "jellyfin-key")
    with open(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), "w") as handle:
        handle.write(key)

    provision_grimmory(args, env)


if __name__ == "__main__":
    sys.exit(main())
