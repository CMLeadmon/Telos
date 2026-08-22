# Restore and Recovery Drill

> **Status:** no operational separate-host recovery drill is available. The
> catalog's `separate-host-restore` external-host gate belongs to Phase 4 and
> remains blocked until that phase supplies the recovery implementation and
> evidence.

## Current behavior

- `scripts/verify-restore.sh --manifest <path> --node-schema <n>
  --node-checksum <hex>` validates a supplied manifest's components, schema,
  and checksum set. It does not restore a node.
- `scripts/restore-drill.sh --fixture --backup-epoch <s> --restore-start <s>
  --restore-end <s> [--out <path>]` computes a fixture RPO/RTO record from
  supplied timestamps for tests. It does not provision, restore, or probe a
  recovery host.
- `scripts/restore-drill.sh --out <path>` without `--fixture` exits 3.
- `scripts/drills/recovery.sh` emits `not_run` with exit 3 until the Phase 4
  `separate-host-restore` gate has a second Ubuntu reference host and recovery
  implementation.

The interim local backup and restore scripts are documented separately in
[`backup-and-restore.md`](./backup-and-restore.md). They are not evidence for
the blocked separate-host recovery gate.

## Blocked target

The Phase 4 target is a candidate-bound, separate-host restore with measured
RPO/RTO and external recovery evidence. Do not treat fixture timestamps,
manifest validation, or local restore mechanics as proof of that target. The
required gate and prerequisites are recorded in
[`ci/phase-gates.json`](../../ci/phase-gates.json).
