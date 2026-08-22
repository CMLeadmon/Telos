#!/usr/bin/env python3
"""Record argv as JSON for gate-runner integration tests."""

import json
import os
import sys


def main():
    if len(sys.argv) < 2:
        print("usage: record_argv.py OUTPUT [ARG ...]", file=sys.stderr)
        return 2
    output = sys.argv[1]
    descriptor = os.open(output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "w", encoding="utf-8") as output_file:
        json.dump(sys.argv[2:], output_file, separators=(",", ":"))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
