# Audiobook Migration Runbook

This document describes the operator procedure for migrating existing Jellyfin audiobook collections to Grimmory without breaking stable links, progress, commentary, or chat shares.

## Prerequisites

1. Phase 1–3 deployments completed and verified.
2. Shared storage volume mounted at `${STORAGE_PATH}` (default `/data/shared`).
3. Database backup taken and restorable (`telos-backup`).
4. Operational maintenance lock acquired (`TELOS_MAINTENANCE_LOCK=1`).

## Execution Stages

### 1. Inventory
Discover and hash all Jellyfin audiobook files into a sealed manifest:
```bash
scripts/migrate-audiobooks.sh inventory --library-id <JELLYFIN_LIBRARY_ID>
```

### 2. Copy
Copy inventoried files into the Telos bookdrop with exact SHA-256 validation:
```bash
scripts/migrate-audiobooks.sh copy
```

### 3. Verify
Verify imported Grimmory books against manifest file-set SHA-256 hashes:
```bash
scripts/migrate-audiobooks.sh verify
```

### 4. Switch
Perform an item-scoped transactional switch of active source and surface:
```bash
scripts/migrate-audiobooks.sh switch
```

### 5. Rollback (Optional)
Revert active source and surface to Jellyfin if necessary:
```bash
scripts/migrate-audiobooks.sh rollback
```

### 6. Cleanup
Remove legacy Jellyfin audiobook files after successful verification and backup proof:
```bash
scripts/migrate-audiobooks.sh cleanup --backup-proof <PROOF_PATH> --confirm-sha <MANIFEST_SHA256>
```
