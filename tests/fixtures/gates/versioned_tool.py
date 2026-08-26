#!/usr/bin/env python3
"""Controlled primary-tool executable for runner version-evidence tests."""

import os
import pathlib
import sys


VERSIONS = {
    "bash": "GNU bash, fixture version 5.2",
    "node": "v24.18.0-fixture",
    "npm": "11.6.2-fixture",
    "npx": "11.6.2-fixture",
    "podman": "podman version 5.0.0-fixture",
}


def main():
    tool = pathlib.Path(sys.argv[0]).name
    if tool not in VERSIONS:
        raise SystemExit(f"unsupported fixture tool: {tool}")
    if sys.argv[1:] == ["--version"]:
        if os.environ.get("TELOS_VERSION_FIXTURE_FAIL") == tool:
            print(f"{tool} fixture version unavailable", file=sys.stderr)
            return 9
        print(VERSIONS[tool])
        return 0
    marker = os.environ.get("TELOS_VERSION_FIXTURE_MARKER")
    if marker:
        pathlib.Path(marker).write_text("gate ran\n", encoding="utf-8")
    print(f"{tool} fixture gate ran")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
