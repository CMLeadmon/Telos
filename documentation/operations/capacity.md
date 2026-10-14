# Telos Capacity Planning and Benchmark Specifications

> **Status:** the capacity drill is implemented (`scripts/run-capacity-drill.sh` → `scripts/lib/capacity_drill.py`,
> `tests/load/telos.js`, `scripts/prepare-capacity-fixture.py`, `scripts/collect-runtime-stats.py`) and covered by
> `tests/operations/capacity_exposure_test.sh`. See "Rehearsal results" for what has actually been run. The `capacity`
> gate needs a frozen candidate on a **disposable** reference node, a separate load host and the full 30 minutes.

## Target baseline

100 registered members, 25 concurrently signed-in; REST p95 ≤ 750 ms, durable mutations p95 ≤ 1 s; request failures < 1 %.

## What the drill does

Run it from a **load host that is not the node**:

```bash
scripts/run-capacity-drill.sh --target-file TARGET.json \
  (--rehearsal --source-commit SHA | --candidate-lock LOCK) --result-out OUT/result.json [--duration 30m]
```

`TARGET.json` is a `drill-target` record whose `capacityTargetFile` names an operator-owned, mode-0600
`capacity-target.json`: `{"schemaVersion": 1, "purpose": "capacity-test", "candidateLockDigest": "sha256:…"|null,
"origin": "https://…", "hostId": "sha256:…"}`. The drill refuses unless its purpose, candidate, origin and machine all
match the run — it never loads a node that was not declared disposable. The credentials file carries the owner, a new
member for the closing journey and the fixture manifest.

1. **Fixture.** Through the public API, 100 members (`capacity-001…100`) and a dedicated chat channel are created; the
   credentials land in a 0700 temp directory outside the repository, the ids in a cleanup manifest. Nothing prints a
   password or an invite. (Creating them takes about 10 minutes at the gateway's hashing cost.)
2. **Load.** The *locked* k6 image (`toolImages.k6` in `release/images.lock`, pulled by digest) runs
   `tests/load/telos.js`: **exactly 25 live member sessions** (one per VU) for 30 minutes; the run is split in four
   phases and at each boundary every VU signs its account out and another in, so all 100 accounts take a turn and there
   are never more than 25 sessions. Every iteration does two validated reads (Chat, Stream with a bounded range request,
   Library, Files, commentary — status **and** shape) and one durable write alternating a chat message and a commentary
   note, paced so the 25 sessions together make **10 writes per second**.
3. **Sampling.** Every 5 seconds, from the node: each container's cgroup (CPU, memory, quota, OOM kills), restart
   counts and health, free storage, Redis memory against `maxmemory`, the gateway's database-pool and stream gauges
   (`telos_db_pool_*`, `telos_active_*` on its internal metrics listener), and live `capacity-*` sessions.
4. **Judgement.** Failed-request rate < 1 %, p95 < 750 ms, mutation p95 < 1000 ms, checks > 99 %, zero semantic
   failures, the thresholds in the summary exactly the required ones (a weakened script cannot pass); no restart, OOM
   kill or health loss; CPU below **80 % of each service's quota in every complete 60 s window**; memory below **85 % of
   its limit at every sample**; free storage at least **max(10 GiB, 10 % of the filesystem)**; no **60 s stretch** with the
   database pool, Redis (`maxmemory 384mb`, `volatile-lru`) or the stream limiter at its maximum; never more than 25
   member sessions, and 25 at the peak.
5. **After.** The member journey runs once more (the node must still work) and every account and the channel are removed
   (`prepare-capacity-fixture.py cleanup`); an incomplete cleanup fails the gate.

A run shorter than 30 minutes is `not_run`, never a pass. A summary without a measurement, a missing sample set or an
unreadable limit fails or is `not_run`. The observation keeps the raw k6 summary and the samples (thinned to about 400
rows); credentials are never recorded.

## Rehearsal results

See the ledger (`docs/implementation/standalone-beta-progress.md`) for the runs made on the reference VMs. A rehearsal
on a 12 GiB VM with a local private CA is not certification evidence.
