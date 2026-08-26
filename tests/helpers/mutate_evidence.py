#!/usr/bin/env python3
"""Create a single deliberately invalid evidence-envelope fixture."""

import argparse
import hashlib
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
    elif name == "mismatched-source-commit":
        source_commit = "b" * 40
        envelope["sourceCommit"] = source_commit
        envelope["subject"] = {
            "kind": "source",
            "digest": "sha256:"
            + hashlib.sha256(source_commit.encode("ascii")).hexdigest(),
        }
    elif name == "mismatched-source-subject":
        envelope["subject"]["digest"] = "sha256:" + ("b" * 64)
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
    elif name == "empty-tool-versions":
        envelope["toolVersions"] = {}
    elif name == "non-string-tool-version":
        envelope["toolVersions"]["python"] = 3
    elif name == "empty-tool-version":
        envelope["toolVersions"]["python"] = ""
    elif name == "unknown-tool-version":
        envelope["toolVersions"]["ruby"] = "3"
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
            "mismatched-source-commit",
            "mismatched-source-subject",
            "string-command",
            "negative-exit",
            "reversed-time",
            "bad-digest",
            "secret-field",
            "empty-tool-versions",
            "non-string-tool-version",
            "empty-tool-version",
            "unknown-tool-version",
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
