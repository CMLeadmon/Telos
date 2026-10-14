#!/usr/bin/env python3
"""Capture credentials from a LIVE node so a later restore can be shown to revoke them.

Rehearsal tooling for the separate-host recovery drill (scripts/lib/recovery_drill.py). Run it
against the node that will be backed up, BEFORE the backup is taken: it signs in as the owner,
creates an invite nobody uses, and registers a device. The browser session cookie, the invite
token and the device's access and refresh tokens are written to a private file; the recovery
drill later proves each one is in the restored database, was revoked by the restore, and is
refused over HTTPS. Nothing is printed except the file path.

    capture-stale-credentials.py --origin https://node.example.net --credentials CREDS.json --out STALE.json

CREDS.json is the journey credentials object ({"owner": {"username", "password"}, ...}).
"""

import argparse
import json
import os
import ssl
import sys
import tempfile
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "scripts" / "lib"))
import browser_node_smoke as smoke  # noqa: E402


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--origin", required=True)
    parser.add_argument("--credentials", required=True)
    parser.add_argument("--out", required=True)
    args = parser.parse_args(argv)
    owner = json.loads(Path(args.credentials).read_text())["owner"]
    context = ssl.create_default_context()
    with tempfile.TemporaryDirectory(prefix="telos-stale.") as directory:
        os.chmod(directory, 0o700)
        session = smoke.Session(args.origin, context, os.path.join(directory, "jar.json"))
        session.json("POST", "/api/v1/auth/login", expected=(200,), json_body={"username": owner["username"], "password": owner["password"]})
        cookies = dict(session.cookies)
        if not cookies:
            raise SystemExit("capture: the login set no cookie")
        invite = session.json("POST", "/api/v1/auth/invites", expected=(200, 201), json_body={"roleId": "Member"})
        device_session = smoke.Session(args.origin, context, os.path.join(directory, "device-jar.json"))
        device = device_session.json("POST", "/api/v1/auth/devices/register", expected=(200, 201), json_body={
            "deviceName": "recovery-drill-device", "platform": "linux", "clientVersion": "0.0.0-rehearsal",
            "username": owner["username"], "password": owner["password"]})
    document = {
        "capturedAt": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "cookies": cookies,
        "inviteToken": invite["invite_token"],
        "deviceAccessToken": device["accessToken"],
        "deviceRefreshToken": device["refreshToken"],
    }
    descriptor = os.open(args.out, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "w") as handle:
        json.dump(document, handle)
    print(args.out)
    return 0


if __name__ == "__main__":
    sys.exit(main())
