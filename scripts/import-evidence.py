#!/usr/bin/env python3
"""Import runner evidence into an immutable, indexed bundle.

    import-evidence.py --candidate-lock PATH --catalog PATH --source-dir PATH [--source-dir PATH ...] --bundle-dir PATH

Verifies every envelope, log, producer result and attachment against the candidate lock, requires exactly the
catalog's required gate set (each passed and fresh, dependencies finished before dependents started), refuses
secret-looking content, copies the files read-only and writes the canonical evidence-index.json. Sign it with
scripts/sign-evidence.sh; certify with scripts/certify-beta.sh.
"""
import os
import sys

sys.dont_write_bytecode = True
sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "lib"))
import evidence_bundle  # noqa: E402

if __name__ == "__main__":
    sys.exit(evidence_bundle.main(["import", *sys.argv[1:]]))
