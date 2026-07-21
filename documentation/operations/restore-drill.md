# Restore and Recovery Drill

> Normative recovery runbook. The mechanics below are implemented and
> unit-tested (`scripts/tests/restore_test.sh`, `TestBackupManifest*`); the
> **live clean-node disaster-recovery drill is a P7 operator gate** because it
> requires a reference recovery environment, the documented DNS/provider
> credentials, and external probes that cannot run in CI.

## Restore order (fail closed)

1. Acquire the shared maintenance lock (`scripts/maintenance-lock.sh`).
2. Fetch/decrypt and verify checksums, release, schema, and migration checksum
   set against this node (`scripts/verify-restore.sh` — a missing component,
   future schema, or checksum mismatch aborts before any active state changes).
3. Restore and probe a candidate generation in isolation (generation-scoped
   temporary database names and same-filesystem generation directories).
4. Stop every writer with checked exits.
5. `journal-begin`: persist and fsync the transition journal
   (`scripts/restore-state.sh`).
6. Atomically activate the candidate by renaming the mode-0600
   active-generation pointer consumed by Compose.
7. Invalidate restored sessions and invites; clear Redis.
8. Run the locked migrator.
9. Reopen internal services and run functional probes: health, authentication,
   recent chat, one authorized Jellyfin item, one EPUB, one PDF (Phase 5 adds
   notifications, annotations, My List, and Watch Party).
10. Only then reopen Traefik.

Any failure after activation automatically reactivates and probes the preserved
prior generation before returning failure; if rollback verification fails,
Traefik stays closed and both generations are preserved for operator recovery.
After an untrappable exit, the next invocation reads the journal and completes
rollback before accepting another operation.

## Recovery objectives

The drill record (`scripts/restore-drill.sh`) captures backup/restore
timestamps, RPO/RTO seconds, release/checksum set, and probe results, and fails
above an 86,400-second RPO or a 14,400-second RTO. For clean-node disaster
recovery the RTO clock starts when host-loss recovery is declared and ends only
after automated firewall and A/AAAA cutover for the documented low-TTL DNS zone
plus valid app/`turn.` certificates pass external HTTPS redirect/SNI/chain,
authenticated WSS, range-media, voice-join, and TURN probes. A same-host-only
restore cannot satisfy the beta RTO claim.
