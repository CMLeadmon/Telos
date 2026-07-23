# Telos Upgrade and Rollback Operations Manual

This document details the automated node upgrade and rollback procedures across Telos releases.

## Upgrade Procedure
1. Acquire node-wide orchestration lock.
2. Produce atomic pre-upgrade database and volume snapshot backup.
3. Apply database migrations sequentially.
4. Update service container images and perform health checks.

## Rollback Procedure
1. Verify database schema compatibility with target prior version.
2. If compatible, apply fast in-place binary rollback.
3. If incompatible, perform full database and storage restore.
