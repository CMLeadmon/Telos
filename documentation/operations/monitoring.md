# Monitoring

A node watches itself with an internal Prometheus. There is no public metrics or
Prometheus endpoint, port or route anywhere: the operator reads status through
`telos status` and `scripts/verify-monitoring.sh`.

## How it is wired

```text
telos-core ──(telos-observability, internal)── telos-prometheus
   :9090 /metrics                                 scrapes every 15 s, keeps 15 days or 1 GB
```

* The gateway serves `/metrics` from a **second listener** (`METRICS_ADDR`, default `:9090`)
  that is not part of the public mux. It answers `GET /metrics` only (404 for every other
  path, 405 for other methods) and only to peers inside `METRICS_ALLOW_CIDRS` (default the
  `telos-observability` subnet `10.202.0.0/24`, plus loopback); anyone else gets 403 before a
  metric is gathered. It starts after the gateway's dependencies and stops with the main
  server.
* `telos-observability` is an `internal` network joined by the gateway and Prometheus only. It
  has no host port and no route out. Prometheus is the locked image, non-root, read-only, with
  no published port, a bounded data volume and its lifecycle/admin APIs off.

## What is exported

| Series | Meaning |
|---|---|
| `telos_http_requests_total`, `telos_http_request_duration_seconds` | by `method` (nine fixed values, else `other`) and `code_class` (`2xx`…`5xx`) — never a path |
| `telos_dependency_up{service}` | 1 when a readiness check is ok; `service` ∈ postgres, redis, outbox, storage, jellyfin, grimmory |
| `telos_active_sockets` | live WebSockets, computed from the registry on every scrape |
| `telos_outbox_queue_size` | pending outbox entries |
| `telos_auth_throttled_total` | throttled sign-in attempts |
| `telos_backup_last_success_timestamp_seconds`, `telos_backup_last_result`, `telos_backup_status_valid` | read from `<node>/ops/last-backup.prom` |
| `telos_catalog_*` | catalog reconciliation outcomes by provider |

**Privacy.** Labels come from a closed vocabulary (`method`, `code_class`, `service`,
`provider`, `outcome`, `operation`, `result`). No username, user ID, channel, item, query,
host, token, address or raw path is ever a label; `backend/metrics_server_test.go` fails if
one appears, and `scripts/verify-monitoring.sh --source` rejects a new label name.

**Backup status.** `telos backup` writes `last-backup.prom` atomically into `<node>/ops/`,
which the gateway mounts read-only. The gateway reads exactly two declared series from it.
A missing, oversize, malformed, duplicated or implausible file is reported as
`telos_backup_status_valid 0` — never as labels, and the gateway accepts no backup data over
the network.

## Alerts

`config/prometheus/alerts.yml`; each alert has a severity and a runbook section in
[incidents.md](incidents.md): `TelosScrapeDown`, `TelosDependencyDown`,
`TelosHighServerErrorRatio` (5%+ with minimum traffic), `TelosOutboxBacklog` (warning) and
`TelosOutboxBacklogCritical`, `TelosAuthThrottleSurge`, `TelosBackupFailed`,
`TelosBackupStale` (24 h) and `TelosBackupStatusMissing` (warning after 24 h).

## Verifying

```text
scripts/verify-monitoring.sh --source     # configuration, routing, privacy, promtool
sudo scripts/verify-monitoring.sh --runtime [--target-file FILE]
```

`--source` runs `promtool check config` and `check rules` from the locked Prometheus image
(or a binary named by `PROMTOOL`); without either it still checks the rest but reports
`not_run`, never a pass. `--runtime` asks the running Prometheus whether the target is up
and was scraped within three intervals, every rule loaded healthily, no **critical** alert
is firing and every required series has data. A result suitable as certification evidence
additionally needs a frozen candidate and an installed generation receipt; a bare run is a
rehearsal and says so.
