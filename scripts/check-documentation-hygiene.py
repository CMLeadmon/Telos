#!/usr/bin/env python3
"""Check the documentation hygiene rules declared in the root agent guide."""

import argparse
import pathlib
import re
import sys


REPO_ROOT = pathlib.Path(__file__).resolve().parent.parent
TARGETS = (REPO_ROOT / "documentation", REPO_ROOT / "AGENTS.md")
PATTERNS = {
    "citations": re.compile(r"cite:"),
    "placeholders": re.compile(r"TODO|TBD"),
}


def iter_files():
    for target in TARGETS:
        if target.is_dir():
            yield from sorted(path for path in target.rglob("*") if path.is_file())
        elif target.is_file():
            yield target
        else:
            raise OSError(f"required documentation target is missing: {target}")


def check(pattern):
    found = False
    for path in iter_files():
        with path.open(encoding="utf-8") as input_file:
            for line_number, line in enumerate(input_file, start=1):
                if pattern.search(line):
                    relative_path = path.relative_to(REPO_ROOT)
                    print(f"{relative_path}:{line_number}:{line.rstrip()}")
                    found = True
    return 1 if found else 0


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("check", choices=sorted(PATTERNS))
    args = parser.parse_args(argv)
    try:
        return check(PATTERNS[args.check])
    except (OSError, UnicodeError) as error:
        print(f"documentation-hygiene: {error}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
