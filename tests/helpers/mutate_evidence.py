#!/usr/bin/env python3
"""Create a single deliberately invalid evidence-envelope fixture."""

import argparse
import json


def mutate(envelope, name):
    if name == "missing-gate":
        del envelope["gateId"]
    elif name == "unknown-status":
        envelope["status"] = "unknown"
    elif name == "mismatched-candidate":
        envelope["candidateLockDigest"] = (
            "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
        )
    elif name == "string-command":
        envelope["command"] = "printf passed"
    elif name == "negative-exit":
        envelope["exitCode"] = -1
    elif name == "reversed-time":
        envelope["finishedAt"] = "2026-08-20T09:59:59Z"
    elif name == "bad-digest":
        envelope["outputDigest"] = "sha256:bad"
    elif name == "secret-field":
        envelope["toolVersions"]["tokenSource"] = "fixture"
    else:
        raise ValueError(f"unknown mutation: {name}")
    return envelope


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "mutation",
        choices=(
            "missing-gate",
            "unknown-status",
            "mismatched-candidate",
            "string-command",
            "negative-exit",
            "reversed-time",
            "bad-digest",
            "secret-field",
        ),
    )
    parser.add_argument("input")
    parser.add_argument("output")
    args = parser.parse_args()

    with open(args.input, encoding="utf-8") as source:
        envelope = json.load(source)
    with open(args.output, "w", encoding="utf-8") as destination:
        json.dump(mutate(envelope, args.mutation), destination, sort_keys=True)
        destination.write("\n")


if __name__ == "__main__":
    main()
