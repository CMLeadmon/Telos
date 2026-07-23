# Telos Release Non-Circular Hash Directed Acyclic Graph (DAG)

```
[Payload Files] -> (artifacts.json) -> (release-manifest.json) -> (SHA256SUMS) -> (candidate-lock.json)
```

1. `artifacts.json`: Contains sha256 checksums of payload files (`telos-core.oci.tar`, `compose.yaml`, SBOMs, runbooks, legal files).
2. `release-manifest.json`: Includes `artifacts.json` hash and metadata.
3. `SHA256SUMS`: Hashes payload files + `artifacts.json` + `release-manifest.json`.
4. `candidate-lock.json`: Hashes all candidate files and signature bundles.
