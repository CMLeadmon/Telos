# Telos release hash graph

The graph is directed and acyclic. A node may only name nodes to its left;
nothing names itself or anything downstream. `scripts/lib/release_graph.py`
owns the one canonical serializer and validator; every document below has a
closed schema in this directory (`*.schema.json`) that is executed, not just
declared (`scripts/lib/schema_check.py`).

```
payload bytes + artifact-bound SBOMs
  -> artifact-index.json          (path, kind, platform, size, sha256, OCI manifest digest)
  -> release-manifest.json        (names the artifact-index bytes; version, source, build-input locks)
  -> candidate-lock.json          (names index + manifest bytes and every payload digest)
  -> gate result / envelope v2    (name the candidate-lock digest; written by the one runner)
  -> evidence-index.json          (names immutable envelopes, logs, results and attachments)
  -> detached SSH signatures and certification.json
```

Rules (checked by `release_graph.py verify` and the mutation tests in
`tests/operations/release_graph_test.py`):

- Canonical JSON is UTF-8, sorted keys, compact separators, integers only, one
  trailing LF; duplicate keys, NaN/Infinity, floats and BOMs are rejected.
- `sha256:<hex>` digests are used everywhere except fields a schema names as a
  raw `sha256`. The candidate-lock digest is the digest of its raw canonical bytes.
- Payload paths are safe relative regular files: no traversal, absolute paths,
  symlinks or special files, and never a downstream node (the graph documents,
  `SHA256SUMS`, evidence, certification, signatures).
- `SHA256SUMS` lists every file except itself; every file in a candidate root is
  either a graph document, `SHA256SUMS`, or named by the artifact index.
- `generation.json` inside the payload carries no candidate digest. The installed
  receipt that binds a generation to a candidate is written to node state at
  install time and is observed evidence, not payload.
- A frozen artifact is never replaced; a fix needs a new candidate and new evidence.
