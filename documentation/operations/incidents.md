# Incident runbooks

Each alert in `config/prometheus/alerts.yml` links to a section here. Commands assume the
operator's shell on the node, `sudo telos …`, and Podman; nothing here requires a
public Prometheus UI (there is none). Status of the monitoring itself:
`scripts/verify-monitoring.sh --runtime`.

## TelosScrapeDown

Prometheus cannot reach the gateway's metrics listener (`telos-core:9090`).

1. `sudo telos status` — is `telos-core` healthy? A stopped or crash-looping gateway is
   the usual cause; its logs are `sudo podman logs telos-core`.
2. If the gateway is healthy, the observability network or the allowlist is wrong: the
   listener answers only peers in `METRICS_ALLOW_CIDRS` (default `10.202.0.0/24`, the
   `telos-observability` network). `sudo podman network inspect telos-observability`.
3. `sudo podman logs telos-prometheus` for scrape errors.

## TelosDependencyDown

A readiness check (`postgres`, `redis`, `outbox`, `storage`, `jellyfin` or `grimmory`;
the alert names it in `service`) has been failing for two minutes.

1. `sudo telos status` prints the same per-dependency result the gateway reports at
   `/api/v1/health`.
2. `jellyfin` or `grimmory`: only that module is degraded; Chat and Files keep working.
   Restart the service (`sudo podman restart telos-jellyfin`) and read its logs.
3. `postgres`, `redis`, `storage`: the gateway is unusable. Check the container, the
   disk (`df -h` on the storage path) and the volume.

## Telos high server error ratio

More than 5% of requests over five minutes returned 5xx while the node serves traffic.

1. `sudo podman logs --since 15m telos-core | grep -i error`.
2. Check `TelosDependencyDown` first: most 5xx bursts are one failing dependency.
3. A recent upgrade is the next suspect: `sudo telos` generation tooling can roll back
   (`documentation/operations/upgrade-and-rollback.md`).

## Telos outbox backlog

The outbox (committed events waiting to be delivered to Redis) is above 100 entries
(warning) or 1000 (critical).

1. Redis healthy? `sudo podman exec telos-redis redis-cli -a "$REDIS_PASSWORD" ping`
   (the password is in `node.env`).
2. The gateway log names the dispatcher's last error.
3. A backlog that grows while both are healthy is a defect: keep the logs and the
   `outbox` rows (`SELECT count(*), min(created_at) FROM outbox`) for the report.

## Telos auth throttle surge

Sign-in attempts are being rejected by the rate limiter at an unusual rate.

1. This is usually a credential-guessing run or a misconfigured client retrying.
2. `sudo podman logs --since 30m telos-core | grep too_many_attempts` shows the pace.
3. Nothing is exposed by the alert itself; no user, address or token is a label.

## Telos backup failed

The last backup run ended in failure (`telos_backup_last_result 0`).

1. `sudo journalctl -u telos-backup.service -n 100` has the failure and the repository
   check that ran.
2. Common causes: the off-node repository is unreachable, its credentials rotated, or
   there is no space. Fix, then `sudo telos backup`.
3. See `documentation/operations/backup-and-restore.md`.

## Telos backup stale

No backup has succeeded for 24 hours although a status is being written.

1. The timer may have stopped: `systemctl list-timers telos-backup.timer`.
2. Run `sudo telos backup` by hand and read the result.

## Telos backup status missing

The gateway has not seen a readable `last-backup.prom` for 24 hours.

1. A new node that has never run a backup shows this until the first one completes.
2. Otherwise the file is missing, malformed or larger than 4 KiB: it is rewritten
   atomically by `telos backup` into `${TELOS_NODE_PATH}/ops/`.
